// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

// This is a modified copy from
// https://github.com/cortexproject/cortex/blob/master/pkg/querier/queryrange/split_by_interval.go.

package queryfrontend

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/weaveworks/common/httpgrpc"

	"github.com/thanos-io/thanos/internal/cortex/querier/queryrange"
)

// SplitByIntervalMiddleware creates a new Middleware that splits requests by a given interval.
func SplitByIntervalMiddleware(interval queryrange.IntervalFn, limits queryrange.Limits, merger queryrange.Merger, registerer prometheus.Registerer) queryrange.Middleware {
	return queryrange.MiddlewareFunc(func(next queryrange.Handler) queryrange.Handler {
		return splitByInterval{
			next:     next,
			limits:   limits,
			merger:   merger,
			interval: interval,
			splitByCounter: promauto.With(registerer).NewCounter(prometheus.CounterOpts{
				Namespace: "thanos",
				Name:      "frontend_split_queries_total",
				Help:      "Total number of underlying query requests after the split by interval is applied",
			}),
		}
	})
}

type splitByInterval struct {
	next   queryrange.Handler
	limits queryrange.Limits
	merger queryrange.Merger
	// 子请求时间窗口
	interval queryrange.IntervalFn

	// Metrics.
	splitByCounter prometheus.Counter
}

func (s splitByInterval) Do(ctx context.Context, r queryrange.Request) (queryrange.Response, error) {
	// First we're going to build new requests, one for each day, taking care
	// to line up the boundaries with step.

	// 子请求列表
	reqs, err := splitQuery(r, s.interval(r))
	if err != nil {
		return nil, err
	}
	s.splitByCounter.Add(float64(len(reqs)))

	// 执行子请求
	reqResps, err := queryrange.DoRequests(ctx, s.next, reqs, s.limits)
	if err != nil {
		return nil, err
	}

	// 子响应列表及合并
	resps := make([]queryrange.Response, 0, len(reqResps))
	for _, reqResp := range reqResps {
		resps = append(resps, reqResp.Response)
	}
	response, err := s.merger.MergeResponse(r, resps...)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// splitQuery 将请求以时间窗口(interval)划分为多个子请求, 并返回子请求列表.
func splitQuery(r queryrange.Request, interval time.Duration) ([]queryrange.Request, error) {
	var reqs []queryrange.Request

	// TODO: 这里有一点我有点晕, ThanosQueryRangeRequest 也是 SplitRequest 呀.
	switch tr := r.(type) {
	case *ThanosQueryRangeRequest:
		// Replace @ modifier function to their respective constant values in the query.
		// This way subqueries will be evaluated at the same time as the parent query.
		query, err := queryrange.EvaluateAtModifierFunction(r.GetQuery(), r.GetStart(), r.GetEnd())
		if err != nil {
			return nil, err
		}
		if start := r.GetStart(); start == r.GetEnd() {
			reqs = append(reqs, tr.WithSplitInterval(interval).WithStartEnd(start, start))
		} else {
			for ; start < r.GetEnd(); start = nextIntervalBoundary(start, r.GetStep(), interval) + r.GetStep() {
				end := nextIntervalBoundary(start, r.GetStep(), interval)
				if end+r.GetStep() >= r.GetEnd() {
					end = r.GetEnd()
				}

				reqs = append(reqs, tr.WithSplitInterval(interval).WithQuery(query).WithStartEnd(start, end))
			}
		}
	case SplitRequest:
		// 将时间窗口转换为毫秒值
		dur := int64(interval / time.Millisecond)
		for start := r.GetStart(); start < r.GetEnd(); start = start + dur {
			end := start + dur
			if end > r.GetEnd() {
				end = r.GetEnd()
			}

			reqs = append(reqs, tr.WithSplitInterval(interval).WithStartEnd(start, end))
		}
	default:
		return nil, httpgrpc.Errorf(http.StatusBadRequest, `{"status": "error", "error": "request type %T not supported"}`, r)
	}

	return reqs, nil
}

// Round up to the step before the next interval boundary.
func nextIntervalBoundary(t, step int64, interval time.Duration) int64 {
	msPerInterval := int64(interval / time.Millisecond)
	startOfNextInterval := ((t / msPerInterval) + 1) * msPerInterval
	// ensure that target is a multiple of steps away from the start time
	target := startOfNextInterval - ((startOfNextInterval - t) % step)
	if target == startOfNextInterval {
		target -= step
	}
	return target
}
