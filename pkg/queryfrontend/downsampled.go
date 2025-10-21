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
	next   queryrange.Handler
	merger queryrange.Merger

	// Metrics.
	additionalQueriesCount prometheus.Counter
}

var resolutions = []int64{downsample.ResLevel1, downsample.ResLevel2}

// Do 执行降采样操作. 默认会使用请求中的数据分辨率来获取数据, 但是当该频率出现数据未完全响应时, 即响应数据时间范围未能完全满足
// (start, end) 时, 会自动升高一级分辨率来获取数据, 直至所有分辨率全部尝试或得到所需数据.
// 无论 http 请求是否使用降采样, 只要命令行参数设定--query-range.request-downsampled=true, 就会使用降采样.
func (d downsampled) Do(ctx context.Context, req queryrange.Request) (queryrange.Response, error) {
	tqrr, ok := req.(*ThanosQueryRangeRequest)
	// 检查请求是否为 query range 且是否开启了自动降采样.
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
	// 默认 i = 0 开始循环.
	for i < len(resolutions) {
		if i > 0 {
			// i > 0 表示再一次请求, 但是为什么需要请求?
			d.additionalQueriesCount.Inc()
		}
		// 请求体.
		r := *tqrr
		// 直接发出请求.
		resp, err = d.next.Do(ctx, &r)
		if err != nil {
			return nil, err
		}
		// 将响应添加到响应列表中等待处理.
		resps = append(resps, resp)
		// Set MaxSourceResolution for next request, if any.
		// MaxSourceResolution 要么是在一更大级别的分辨率, 要么是原始值(1h).
		for i < len(resolutions) {
			if tqrr.MaxSourceResolution < resolutions[i] {
				// 只要能进来就说明请求要么是 auto, 要么是 raw data.
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
		case tqrr.Start: // Response not impacted by retention policy.
			// 响应数据中最小时间戳为 start.
			break forLoop
		case -1: // Empty response, retry with higher MaxSourceResolution.
			// 没有数据. 继续请求, 此时请求的是更高的分辨率.
			continue
		default:
			// TODO : 这里的逻辑是啥? 为什么结束时间 = 响应数据中最小时间 - 请求步长.
			tqrr.End = m - tqrr.Step
		}
		if tqrr.Start > tqrr.End {
			break forLoop
		}
	}

	// 合并响应.
	response, err := d.merger.MergeResponse(req, resps...)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// minResponseTime 返回最小时间戳, 前提是返回的数据中数据点必须是时间严格递增. 若无数据则返回 -1.
// 我猜测, 当返回 -1 的时候不一定是没有对应分辨率的数据, 有可能是数据量过大而不返回.
func minResponseTime(r queryrange.Response) int64 {
	var res = r.(*queryrange.PrometheusResponse).Data.Result
	if len(res) == 0 || (len(res[0].Samples) == 0 && len(res[0].Histograms) == 0) {
		return -1
	}

	minTs := int64(math.MaxInt64)

	//TODO: Samples 和 Histograms 不一样? 是不是 Histogram 类型是单独字段保存的?
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
