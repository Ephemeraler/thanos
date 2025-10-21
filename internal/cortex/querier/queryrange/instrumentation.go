// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package queryrange

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/weaveworks/common/instrument"
)

// InstrumentMiddleware 用于记录该层之下操作执行完毕的之间.
func InstrumentMiddleware(name string, metrics *InstrumentMiddlewareMetrics) Middleware {
	// github.com/weaveworks/common/instrument 的使用.
	// instrument.Collector
	// 	type Collector interface {
	//     Register()
	//     Before(ctx context.Context, method string, start time.Time)
	//     After(ctx context.Context, method, statusCode string, start time.Time)
	// }
	//
	// instrument.Collector 有一套推荐的标签约定:
	// - operation: 操作名
	// - statu_code: 响应码
	// - method: 表示 http method 或 grpc 方法名称.
	//
	//
	// instrument.NewHistogramCollector 基于 prometheus.HistogramVec 创建 instrument.Collector.
	// instrument 还提供了 NewHistogramCollectorFromOpts 方法, 可以通过 prometheus.HistogramOpts 创建 instrument.Collector.
	//
	// func instrument.CollectedRequest(ctx context.Context, method string, col instrument.Collector, toStatusCode func(error) string, f func(context.Context) error) error
	// 带计时与可选追踪的包装器. method 参数是在指标中增加额外标签 method=<method>
	// - 记录开始时间(Before函数)
	// - 执行传入的函数(f)
	// - 用 toStatusCode 把返回的 error 映射成状态码字符串(可自定义映射关系)
	// - 计算耗时并调用 After 往直方图中 observe 一次, 带上
	// - 如果全局配置了tracer, 会同时产生 span

	var durationCol instrument.Collector

	// Support the case metrics shouldn't be tracked (ie. unit tests).
	if metrics != nil {
		durationCol = instrument.NewHistogramCollector(metrics.duration)
	} else {
		durationCol = &NoopCollector{}
	}

	return MiddlewareFunc(func(next Handler) Handler {
		return HandlerFunc(func(ctx context.Context, req Request) (Response, error) {
			var resp Response
			err := instrument.CollectedRequest(ctx, name, durationCol, instrument.ErrorCode, func(ctx context.Context) error {
				var err error
				resp, err = next.Do(ctx, req)
				return err
			})
			return resp, err
		})
	})
}

// InstrumentMiddlewareMetrics 作用于检测某中间层的运行指标.
type InstrumentMiddlewareMetrics struct {
	duration *prometheus.HistogramVec
}

// NewInstrumentMiddlewareMetrics 返回 InstrumentMiddlewareMetrics.
func NewInstrumentMiddlewareMetrics(registerer prometheus.Registerer) *InstrumentMiddlewareMetrics {
	return &InstrumentMiddlewareMetrics{
		// func promauto.With(r prometheus.Registerer) promauto.Factory
		// 该函数用于返回 promauto.Factory 类型, 用于根据提供的 Registerer(r) 创建并自动注册 Prometheus Collectors.
		// 若 r == nil, 则创建的 collectors 不会注册到任何 Registerer 中.
		duration: promauto.With(registerer).NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "cortex",
			Name:      "frontend_query_range_duration_seconds",
			Help:      "Total time spent in seconds doing query range requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "status_code"}),
	}
}

// NoopCollector is a noop collector that can be used as placeholder when no metric
// should tracked by the instrumentation.
type NoopCollector struct{}

// Register implements instrument.Collector.
func (c *NoopCollector) Register() {}

// Before implements instrument.Collector.
func (c *NoopCollector) Before(ctx context.Context, method string, start time.Time) {}

// After implements instrument.Collector.
func (c *NoopCollector) After(ctx context.Context, method, statusCode string, start time.Time) {}
