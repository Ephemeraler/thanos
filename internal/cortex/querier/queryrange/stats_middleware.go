// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package queryrange

import (
	"context"

	"github.com/thanos-io/thanos/internal/cortex/querier/stats"
)

// statsMiddleware
type statsMiddleware struct {
	next Handler
	// --query-frontend.force-query-stats, 默认值 false
	forceStats bool
}

// NewStatsMiddleware 返回 Handler 接口类型(statsMiddleware).
func NewStatsMiddleware(forceStats bool) Middleware {
	return MiddlewareFunc(func(next Handler) Handler {
		return statsMiddleware{
			next:       next,
			forceStats: forceStats,
		}
	})
}

// Do 该函数主要有包括两个功能:
// 1. 若 frontend.force-query-stats 参数开启, 则在 Request 中设置 stats 参数.
// 2. 统计执行窗口中返回的 stats, 则将其值设置到 context 中的 Stats 对象中.
func (s statsMiddleware) Do(ctx context.Context, r Request) (Response, error) {
	// 判断是否开启 stats 功能, 若开启则在 Request 中设置 stats 参数.
	if s.forceStats {
		r = r.WithStats("all")
	}
	resp, err := s.next.Do(ctx, r)
	if err != nil {
		return resp, err
	}

	if resp.GetStats() != nil {
		if sts := stats.FromContext(ctx); sts != nil {
			// 这里想要进来, 就表示参数 ctx 一定在此函数之前设置过 Stats 值. 是在 internal/cortex/frontend/transport/handler.go 的 ServerHTTP 方法中初始化为空的.

			// TODO 从这里来看, 一个执行窗口可能是 frontend 将要处理的一个请求, 这个请求不是原始请求, 而是切分之后的.
			sts.SetPeakSamples(max(sts.LoadPeakSamples(), resp.GetStats().Samples.PeakSamples))
			sts.AddTotalSamples(resp.GetStats().Samples.TotalQueryableSamples)
		}
	}

	return resp, err
}
