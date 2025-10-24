// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package queryfrontend

import (
	"context"
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/thanos-io/thanos/internal/cortex/querier/queryrange"
	"github.com/thanos-io/thanos/pkg/compact/downsample"
)

// DownsampledMiddleware creates a new Middleware that requests downsampled data
// should response to original request with auto max_source_resolution not contain data points.
func DownsampledMiddleware(merger queryrange.Merger, registerer prometheus.Registerer) queryrange.Middleware {
	return queryrange.MiddlewareFunc(func(next queryrange.Handler) queryrange.Handler {
		return downsampled{
			next:   next,
			merger: merger,
			additionalQueriesCount: promauto.With(registerer).NewCounter(prometheus.CounterOpts{
				Namespace: "thanos",
				Name:      "frontend_downsampled_extra_queries_total",
				Help:      "Total number of additional queries for downsampled data",
			}),
		}
	})
}

type downsampled struct {
	next                   queryrange.Handler
	merger                 queryrange.Merger
	additionalQueriesCount prometheus.Counter
}

var resolutions = []int64{downsample.ResLevel1, downsample.ResLevel2}

// Do 执行降采样操作. 默认会使用请求中的数据分辨率来获取数据, 但是当该频率出现数据未完全响应时, 即响应数据时间范围未能完全满足
// (start, end) 时, 会自动升高一级分辨率来获取数据, 直至所有分辨率全部尝试或得到所需数据.
// 无论 http 请求是否使用降采样, 只要命令行参数设定--query-range.request-downsampled=true, 就会使用降采样.
func (d downsampled) Do(ctx context.Context, req queryrange.Request) (queryrange.Response, error) {
	tqrr, ok := req.(*ThanosQueryRangeRequest)
	if !ok || !tqrr.AutoDownsampling {
		return d.next.Do(ctx, req)
	}

	var (
		resps = make([]queryrange.Response, 0)
		resp  queryrange.Response
		err   error
		i     int
	)

forLoop:
	// i = [0, 2)
	for i < len(resolutions) {
		if i > 0 {
			// 为什么只有 i > 0 的时候才会执行？
			// 因为当 i = 0 的时候, 一定会执行一次 next. 当 i > 0 时, 说明循环至少已经执行过1次.
			d.additionalQueriesCount.Inc()
		}

		// 为什么是 ThanosQueryRangeRequest 副本?
		// 因为需要保留原始请求的参数, 多次请求的参数都是基于原始参数修改的.
		r := *tqrr

		// 执行下一层.
		resp, err = d.next.Do(ctx, &r)
		if err != nil {
			return nil, err
		}
		resps = append(resps, resp)

		// 找到最小的、且严格大于当前 MaxSourceResolution 的默认分辨率.
		// 将其设置为 MaxSourceResolution
		for i < len(resolutions) {
			if tqrr.MaxSourceResolution < resolutions[i] {
				// 表示当前请求中 MaxSourceResolution < 当前分辨率级别.
				tqrr.AutoDownsampling = false
				tqrr.MaxSourceResolution = resolutions[i]
				break
			}
			i++
		}

		// 解释一下下述swith代码及if代码块在做什么. 虽然我不知道为什么会需要这种行为
		// · · · · · · · ·
		//       m
		// 按正常逻辑从 m 点开始及其之后是本次响应的数据.
		// 若 m 为负数, 表示请求响应没有数据.
		// 若 m == start. 至少表示返回的数据起点满足数据请求的起点, 但是终点这里为什么不判断或者说终点为什么不会有问题我还不清楚.
		// 若 m != start. 可以遇见, 要么 start 在 m 之后, 要么 start 在 m 之前.
		// 当 start 在 m 之后时, 表示请求数据在响应数据中, 就不需要再请求更大分辨率的数据了. 所以 if 就判断不需要再继续请求数据了.
		// 当 start 在 m 之前时, 意味着 (start, m)之前的数据没有请求回来, 需要再次请求. 但是这里需要值得注意的是, 如果 start 没有在前一个数据点之前
		// 也就是 m - step 也不需要请求数据了. 所以 if 在这里一箭双雕.

		// 找到响应中最小时间戳, 找不到就说明当前分辨率没有返回数据.
		m := minResponseTime(resp)
		switch m {
		case tqrr.Start:
			// 返回的数据最小时间点正好是请求的开始时间.
			break forLoop
		case -1:
			// 本次请求的分辨率没有数据, 继续请求一次更大分辨率或已经无更大分辨率就退出.
			continue
		default:
			// 只返回了部分数据, [start, m) 之间的数据还没有返回回来.
			tqrr.End = m - tqrr.Step
		}
		// 判断 m - step 是不是已经超过边界了. 之所以有这个问题，得研究后面。查询[start, end]到底会从start点开始返回吗？有可能不会，因为对齐的原因.
		if tqrr.Start > tqrr.End {
			break forLoop
		}
		// 表示当前分辨率中缺失部分数据, 所以向更高分辨率去请求.
	}

	// 合并响应.
	response, err := d.merger.MergeResponse(req, resps...)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// minResponseTime 返回最小时间戳, 若无数据则返回 -1.
func minResponseTime(r queryrange.Response) int64 {
	// res 是 Matrix.
	var res = r.(*queryrange.PrometheusResponse).Data.Result
	if len(res) == 0 || (len(res[0].Samples) == 0 && len(res[0].Histograms) == 0) {
		// 没有任何 Metric || 有 Metric 但是没有样本点 || 有 Metric 但是没有样本点.
		return -1
	}

	minTs := int64(math.MaxInt64)
	for _, sampleStream := range res {
		if len(sampleStream.Samples) > 0 {
			if ts := sampleStream.Samples[0].TimestampMs; ts < minTs {
				minTs = ts
			}
		}

		if len(sampleStream.Histograms) > 0 {
			if ts := sampleStream.Histograms[0].Timestamp; ts < minTs {
				minTs = ts
			}
		}
	}

	return minTs
}
