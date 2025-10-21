// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package http

import (
	"context"
	"net/http"
	"net/http/pprof"

	"github.com/felixge/fgprof"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	toolkit_web "github.com/prometheus/exporter-toolkit/web"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/thanos-io/thanos/pkg/component"
	"github.com/thanos-io/thanos/pkg/logutil"
	"github.com/thanos-io/thanos/pkg/prober"
)

// A Server defines parameters for serve HTTP requests, a wrapper around http.Server.
type Server struct {
	logger log.Logger
	comp   component.Component // 所属组件
	prober *prober.HTTPProbe   // 组件服务探活器

	mux *http.ServeMux // Multiplexer
	srv *http.Server   // HTTP server

	opts options // 也不一定每个组件都使用到这里面的选项, 保持扩展性.
}

// New 创建 http server, 并默认注册 observability api.
func New(logger log.Logger, reg *prometheus.Registry, comp component.Component, prober *prober.HTTPProbe, opts ...Option) *Server {
	// 配置 http server 的选项.
	options := options{}
	for _, o := range opts {
		o.apply(&options)
	}

	// 创建 multiplexer 或使用可选配置中的 multiplexer.
	mux := http.NewServeMux()
	if options.mux != nil {
		mux = options.mux
	}

	// 注册 observability 相关的路由.
	registerMetrics(mux, reg)
	registerProbes(mux, prober, logger)
	registerProfiler(mux)

	// 根据配置选项决定是否启用 http2, 并配置 multiplexer.
	var h http.Handler
	if options.enableH2C {
		h2s := &http2.Server{}
		h = h2c.NewHandler(mux, h2s)
	} else {
		h = mux
	}

	return &Server{
		logger: log.With(logger, "service", "http/server", "component", comp.String()),
		comp:   comp,
		prober: prober,
		mux:    mux,
		srv:    &http.Server{Addr: options.listen, Handler: h},
		opts:   options,
	}
}

// ListenAndServe 启动监听服务
func (s *Server) ListenAndServe() error {
	level.Info(s.logger).Log("msg", "listening for requests and metrics", "address", s.opts.listen)
	// 验证 TLS 配置文件是否存在, 配置是否有效.
	err := toolkit_web.Validate(s.opts.tlsConfigPath)
	if err != nil {
		return errors.Wrap(err, "server could not be started")
	}

	flags := &toolkit_web.FlagConfig{
		WebListenAddresses: &([]string{s.opts.listen}),
		WebSystemdSocket:   ofBool(false),
		WebConfigFile:      &s.opts.tlsConfigPath,
	}

	return errors.Wrap(toolkit_web.ListenAndServe(s.srv, flags, logutil.GoKitLogToSlog(s.logger)), "serve HTTP and metrics")
}

// ShutDown 关闭监听服务.
func (s *Server) Shutdown(err error) {
	level.Info(s.logger).Log("msg", "internal server is shutting down", "err", err)
	// 用于判断退出的原因是主动调用, 还是服务启动失败.
	if err == http.ErrServerClosed {
		level.Warn(s.logger).Log("msg", "internal server closed unexpectedly")
		return
	}

	// 是否优雅关闭.
	if s.opts.gracePeriod == 0 {
		s.srv.Close()
		level.Info(s.logger).Log("msg", "internal server is shutdown", "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.opts.gracePeriod)
	defer cancel()

	if err := s.srv.Shutdown(ctx); err != nil {
		level.Error(s.logger).Log("msg", "internal server shut down failed", "err", err)
		return
	}
	level.Info(s.logger).Log("msg", "internal server is shutdown gracefully", "err", err)
}

// Handle 注册路由、绑定处理函数.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// registerProfiler 注册 pprof 路由, 用于性能分析.
func registerProfiler(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/fgprof", fgprof.Handler())
}

// registerMetrics 注册 /metrics 路由, 用于暴露 Prometheus 指标.
func registerMetrics(mux *http.ServeMux, g prometheus.Gatherer) {
	if g != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(g, promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		}))
	}
}

// registerProbes 注册健康检查和就绪检查的路由.
func registerProbes(mux *http.ServeMux, p *prober.HTTPProbe, logger log.Logger) {
	if p != nil {
		mux.Handle("/-/healthy", p.HealthyHandler(logger))
		mux.Handle("/-/ready", p.ReadyHandler(logger))
	}
}

// Helper for exporter toolkit FlagConfig.
func ofBool(i bool) *bool {
	return &i
}
