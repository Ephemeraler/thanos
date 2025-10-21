// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package queryrange

import (
	"context"
	"errors"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/weaveworks/common/httpgrpc"

	util_log "github.com/thanos-io/thanos/internal/cortex/util/log"
)

type RetryMiddlewareMetrics struct {
	retriesCount prometheus.Histogram
}

func NewRetryMiddlewareMetrics(registerer prometheus.Registerer) *RetryMiddlewareMetrics {
	return &RetryMiddlewareMetrics{
		retriesCount: promauto.With(registerer).NewHistogram(prometheus.HistogramOpts{
			Namespace: "cortex",
			Name:      "query_frontend_retries",
			Help:      "Number of times a request is retried.",
			Buckets:   []float64{0, 1, 2, 3, 4, 5},
		}),
	}
}

type retry struct {
	log        log.Logger
	next       Handler
	maxRetries int

	metrics *RetryMiddlewareMetrics
}

// NewRetryMiddleware returns a middleware that retries requests if they
// fail with 500 or a non-HTTP error.
func NewRetryMiddleware(log log.Logger, maxRetries int, metrics *RetryMiddlewareMetrics) Middleware {
	if metrics == nil {
		metrics = NewRetryMiddlewareMetrics(nil)
	}

	return MiddlewareFunc(func(next Handler) Handler {
		return retry{
			log:        log,
			next:       next,
			maxRetries: maxRetries,
			metrics:    metrics,
		}
	})
}

// Do 如果遇到 HTTP 5xx 错误或非 HTTP 错误, 则重试.
// 5xx 表示服务端出现错误
// 非 HTTP 错误表示请求未能成功处理, 可能是网络问题或其他原因.
func (r retry) Do(ctx context.Context, req Request) (Response, error) {
	tries := 0
	defer func() { r.metrics.retriesCount.Observe(float64(tries)) }()

	var lastErr error
	for ; tries < r.maxRetries; tries++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// 执行
		resp, err := r.next.Do(ctx, req)
		if err == nil {
			return resp, nil
		}

		// 请求关闭无需重试.
		if errors.Is(err, context.Canceled) {
			return nil, err
		}

		// Retry if we get a HTTP 500 or a non-HTTP error.
		httpResp, ok := httpgrpc.HTTPResponseFromError(err)
		if !ok || httpResp.Code/100 == 5 {
			// 如果是非 HTTP错误 或 5xx 错误, 则重试.
			// 没有转换成功 或 是 HTTP 5xx 错误
			lastErr = err
			level.Error(util_log.WithContext(ctx, r.log)).Log("msg", "error processing request", "try", tries, "err", err)
			continue
		}

		return nil, err
	}
	return nil, lastErr
}
