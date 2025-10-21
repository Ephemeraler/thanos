// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package http

import (
	"net/http"
	"time"
)

// options http server 的可选配置项.
type options struct {
	gracePeriod   time.Duration
	listen        string
	tlsConfigPath string
	mux           *http.ServeMux
	enableH2C     bool
}

// Option 选项模式, 为了可选配置.
type Option interface {
	apply(*options)
}

// optionFunc 是 Option 的具体实现.
type optionFunc func(*options)

// apply 是为了执行 optionFunc. 实际上时 apply 收到的参数传递给 optionFunc 作为参数.
func (f optionFunc) apply(o *options) {
	f(o)
}

// WithGracePeriod 配置 HTTP 服务器的优雅关闭时间.
func WithGracePeriod(t time.Duration) Option {
	return optionFunc(func(o *options) {
		o.gracePeriod = t
	})
}

// WithListen 配置 HTTP Server 的监听地址.
func WithListen(s string) Option {
	return optionFunc(func(o *options) {
		o.listen = s
	})
}

// WithTLSConfig 配置 TLS 证书的路径.
func WithTLSConfig(tls string) Option {
	return optionFunc(func(o *options) {
		o.tlsConfigPath = tls
	})
}

// WithEnableH2C 配置是否启用 H2C.
func WithEnableH2C(enableH2C bool) Option {
	return optionFunc(func(o *options) {
		o.enableH2C = enableH2C
	})
}

// WithMux 配置 HTTP 服务器的 ServeMux.
func WithMux(mux *http.ServeMux) Option {
	return optionFunc(func(o *options) {
		o.mux = mux
	})
}
