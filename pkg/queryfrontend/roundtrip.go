// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package queryfrontend

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/thanos-io/thanos/pkg/tenancy"

	"github.com/thanos-io/thanos/pkg/querysharding"

	"github.com/go-kit/log"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/thanos-io/thanos/internal/cortex/querier/queryrange"
	"github.com/thanos-io/thanos/internal/cortex/util/validation"
)

const (
	// labels used in metrics.
	rangeQueryOp   = "query_range"
	instantQueryOp = "query"
	labelNamesOp   = "label_names"
	labelValuesOp  = "label_values"
	seriesOp       = "series"
)

var labelValuesPattern = regexp.MustCompile("/api/v1/label/.+/values$")

// NewTripperware returns a Tripperware which sends requests to different sub tripperwares based on the query type.
func NewTripperware(config Config, reg prometheus.Registerer, logger log.Logger) (queryrange.Tripperware, error) {
	var (
		queryRangeLimits, labelsLimits queryrange.Limits
		err                            error
	)

	// 命令行参数中的限制配置, 当前仅支持 query-range.max-query-length, query-range.max-query-parallelism, query-range.response-cache-max-freshness
	if config.QueryRangeConfig.Limits != nil {
		queryRangeLimits, err = validation.NewOverrides(*config.QueryRangeConfig.Limits, nil)
		if err != nil {
			return nil, errors.Wrap(err, "initialize query range limits")
		}
	}

	// 命令行参数中的限制配置, 当前仅支持 labels.max-query-parallelism, labels.response-cache-max-freshness
	if config.LabelsConfig.Limits != nil {
		labelsLimits, err = validation.NewOverrides(*config.LabelsConfig.Limits, nil)
		if err != nil {
			return nil, errors.Wrap(err, "initialize labels limits")
		}
	}

	// 创建 请求/响应 Codec.
	queryRangeCodec := NewThanosQueryRangeCodec(config.QueryRangeConfig.PartialResponseStrategy)
	labelsCodec := NewThanosLabelsCodec(config.LabelsConfig.PartialResponseStrategy, config.DefaultTimeRange)
	queryInstantCodec := NewThanosQueryInstantCodec(config.QueryRangeConfig.PartialResponseStrategy)

	// 创建 query range tripperware.
	queryRangeTripperware, err := newQueryRangeTripperware(
		config.QueryRangeConfig,
		queryRangeLimits,
		queryRangeCodec,
		config.NumShards,
		config.CortexHandlerConfig.QueryStatsEnabled,
		prometheus.WrapRegistererWith(prometheus.Labels{"tripperware": "query_range"}, reg), logger, config.ForwardHeaders)
	if err != nil {
		return nil, err
	}

	// 创建 labels tripperware.
	labelsTripperware, err := newLabelsTripperware(config.LabelsConfig, labelsLimits, labelsCodec,
		prometheus.WrapRegistererWith(prometheus.Labels{"tripperware": "labels"}, reg), logger, config.ForwardHeaders)
	if err != nil {
		return nil, err
	}

	// 创建 instant query tripperware.
	queryInstantTripperware := newInstantQueryTripperware(
		config.NumShards,
		queryRangeLimits,
		queryInstantCodec,
		prometheus.WrapRegistererWith(prometheus.Labels{"tripperware": "query_instant"}, reg),
		config.ForwardHeaders,
		config.CortexHandlerConfig.QueryStatsEnabled,
	)
	return func(next http.RoundTripper) http.RoundTripper {
		tripper := newRoundTripper(
			next,
			queryRangeTripperware(next),
			labelsTripperware(next),
			queryInstantTripperware(next),
			reg,
		)
		return tenancy.InternalTenancyConversionTripper(config.TenantHeader, config.TenantCertField, tripper)
	}, nil
}

// roundTripper 实现 http.RoundTripper 接口, 其主要用于识别 Request 并将其分发到对应的 http.RoundTripper.
type roundTripper struct {
	next         http.RoundTripper      // 下一层 http.RoundTripper, 处理未匹配上的请求.
	queryInstant http.RoundTripper      // 处理 /api/v1/query 请求
	queryRange   http.RoundTripper      // 处理 /api/v1/query_range 请求
	labels       http.RoundTripper      // 处理 /api/v1/labels, /api/v1/label/.+/values, /api/v1/series 请求
	queriesCount *prometheus.CounterVec // 本层 Metrics
}

// newRoundTripper 创建封装 Tripper, 注册查询计数器.
func newRoundTripper(next, queryRange, metadata, queryInstant http.RoundTripper, reg prometheus.Registerer) roundTripper {
	r := roundTripper{
		next:         next,
		queryInstant: queryInstant,
		queryRange:   queryRange,
		labels:       metadata,
		queriesCount: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "thanos_query_frontend_queries_total", // TODO 该层在哪执行决定了该指标的意义.
			Help: "Total queries passing through query frontend",
		}, []string{"op"}),
	}

	r.queriesCount.WithLabelValues(instantQueryOp)
	r.queriesCount.WithLabelValues(rangeQueryOp)
	r.queriesCount.WithLabelValues(labelNamesOp)
	r.queriesCount.WithLabelValues(labelValuesOp)
	r.queriesCount.WithLabelValues(seriesOp)
	return r
}

func (r roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	switch op := getOperation(req); op {
	case instantQueryOp:
		r.queriesCount.WithLabelValues(instantQueryOp).Inc()
		return r.queryInstant.RoundTrip(req)
	case rangeQueryOp:
		r.queriesCount.WithLabelValues(rangeQueryOp).Inc()
		return r.queryRange.RoundTrip(req)
	case labelNamesOp, labelValuesOp, seriesOp:
		r.queriesCount.WithLabelValues(op).Inc()
		return r.labels.RoundTrip(req)
	default:
	}

	return r.next.RoundTrip(req)
}

// getOperation 根据请求的 URL 返回对应请求操作类型.
func getOperation(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodPost {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
			return instantQueryOp
		case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
			return rangeQueryOp
		case strings.HasSuffix(r.URL.Path, "/api/v1/labels"):
			return labelNamesOp
		case strings.HasSuffix(r.URL.Path, "/api/v1/series"):
			return seriesOp
		default:
			if labelValuesPattern.MatchString(r.URL.Path) {
				return labelValuesOp
			}
		}
	}

	return ""
}

// newQueryRangeTripperware returns a Tripperware for range queries configured with middlewares of
// limit, step align, downsampled, split by interval, cache requests and retry.
func newQueryRangeTripperware(
	config QueryRangeConfig,
	limits queryrange.Limits,
	codec *queryRangeCodec,
	numShards int,
	forceStats bool,
	reg prometheus.Registerer,
	logger log.Logger,
	forwardHeaders []string,
) (queryrange.Tripperware, error) {
	// limit 中间层.
	queryRangeMiddleware := []queryrange.Middleware{queryrange.NewLimitsMiddleware(limits)}
	m := queryrange.NewInstrumentMiddlewareMetrics(reg)

	queryRangeMiddleware = append(
		queryRangeMiddleware,
		queryrange.NewStatsMiddleware(forceStats),
	)

	// 是否开启查询步长对齐.
	if config.AlignRangeWithStep {
		queryRangeMiddleware = append(
			queryRangeMiddleware,
			queryrange.InstrumentMiddleware("step_align", m),
			queryrange.StepAlignMiddleware,
		)
	}

	// 开启降采样功能.
	if config.RequestDownsampled {
		queryRangeMiddleware = append(
			queryRangeMiddleware,
			queryrange.InstrumentMiddleware("downsampled", m),
			DownsampledMiddleware(codec, reg),
		)
	}

	if config.SplitQueriesByInterval != 0 || config.MinQuerySplitInterval != 0 {
		queryIntervalFn := dynamicIntervalFn(config)

		queryRangeMiddleware = append(
			queryRangeMiddleware,
			queryrange.InstrumentMiddleware("split_by_interval", m),
			SplitByIntervalMiddleware(queryIntervalFn, limits, codec, reg),
		)
	}

	if numShards > 0 {
		analyzer := querysharding.NewQueryAnalyzer()
		queryRangeMiddleware = append(
			queryRangeMiddleware,
			PromQLShardingMiddleware(analyzer, numShards, limits, codec, reg),
		)
	}

	if config.ResultsCacheConfig != nil {
		queryCacheMiddleware, _, err := queryrange.NewResultsCacheMiddleware(
			logger,
			*config.ResultsCacheConfig,
			newThanosCacheKeyGenerator(),
			limits,
			codec,
			queryrange.PrometheusResponseExtractor{},
			nil,
			shouldCache,
			reg,
		)
		if err != nil {
			return nil, errors.Wrap(err, "create results cache middleware")
		}

		queryRangeMiddleware = append(
			queryRangeMiddleware,
			queryrange.InstrumentMiddleware("results_cache", m),
			queryCacheMiddleware,
		)
	}

	if config.MaxRetries > 0 {
		queryRangeMiddleware = append(
			queryRangeMiddleware,
			queryrange.InstrumentMiddleware("retry", m),
			queryrange.NewRetryMiddleware(logger, config.MaxRetries, queryrange.NewRetryMiddlewareMetrics(reg)),
		)
	}

	return func(next http.RoundTripper) http.RoundTripper {
		rt := queryrange.NewRoundTripper(next, codec, forwardHeaders, queryRangeMiddleware...)
		return queryrange.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return rt.RoundTrip(r)
		})
	}, nil
}

func dynamicIntervalFn(config QueryRangeConfig) queryrange.IntervalFn {
	return func(r queryrange.Request) time.Duration {
		// Use static interval, by default.
		if config.SplitQueriesByInterval != 0 {
			return config.SplitQueriesByInterval
		}

		queryInterval := time.Duration(r.GetEnd()-r.GetStart()) * time.Millisecond
		// If the query is multiple of max interval, we use the max interval to split.
		if queryInterval/config.MaxQuerySplitInterval >= 2 {
			return config.MaxQuerySplitInterval
		}

		if queryInterval > config.MinQuerySplitInterval {
			// If the query duration is less than max interval, we split it equally in HorizontalShards.
			return time.Duration(queryInterval.Milliseconds()/config.HorizontalShards) * time.Millisecond
		}

		return config.MinQuerySplitInterval
	}
}

// newLabelsTripperware returns a Tripperware for labels and series requests
// configured with middlewares of split by interval and retry.
func newLabelsTripperware(
	config LabelsConfig,
	limits queryrange.Limits,
	codec *labelsCodec,
	reg prometheus.Registerer,
	logger log.Logger,
	forwardHeaders []string,
) (queryrange.Tripperware, error) {
	labelsMiddleware := []queryrange.Middleware{}
	m := queryrange.NewInstrumentMiddlewareMetrics(reg)

	// labels.split-interval
	queryIntervalFn := func(_ queryrange.Request) time.Duration {
		return config.SplitQueriesByInterval
	}

	// 若 labels.split-interval 为0, 则不启用 SplitByIntervalMiddleware.
	if config.SplitQueriesByInterval != 0 {
		labelsMiddleware = append(
			labelsMiddleware,
			queryrange.InstrumentMiddleware("split_interval", m),
			SplitByIntervalMiddleware(queryIntervalFn, limits, codec, reg),
		)
	}

	if config.ResultsCacheConfig != nil {
		queryCacheMiddleware, _, err := queryrange.NewResultsCacheMiddleware(
			logger,
			*config.ResultsCacheConfig,
			newThanosCacheKeyGenerator(),
			limits,
			codec,
			ThanosResponseExtractor{},
			nil,
			shouldCache,
			reg,
		)
		if err != nil {
			return nil, errors.Wrap(err, "create results cache middleware")
		}

		labelsMiddleware = append(
			labelsMiddleware,
			queryrange.InstrumentMiddleware("results_cache", m),
			queryCacheMiddleware,
		)
	}

	if config.MaxRetries > 0 {
		labelsMiddleware = append(
			labelsMiddleware,
			queryrange.InstrumentMiddleware("retry", m),
			queryrange.NewRetryMiddleware(logger, config.MaxRetries, queryrange.NewRetryMiddlewareMetrics(reg)),
		)
	}
	return func(next http.RoundTripper) http.RoundTripper {
		rt := queryrange.NewRoundTripper(next, codec, forwardHeaders, labelsMiddleware...)

		// 我一直在想问什么上面那行代码已经返回了 http.RoundTripper, 这里还要用 RoundTripFunc 再在外部封装一层呢.
		// 或许吧, 可能是想抛弃 rt 的 Handler 接口类型.
		return queryrange.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return rt.RoundTrip(r)
		})
	}, nil
}

// newInstantQueryTripperware 返回 http.RoundTripper 构造器.
func newInstantQueryTripperware(
	numShards int,
	limits queryrange.Limits,
	codec queryrange.Codec,
	reg prometheus.Registerer,
	forwardHeaders []string,
	forceStats bool,
) queryrange.Tripperware {
	var instantQueryMiddlewares []queryrange.Middleware
	m := queryrange.NewInstrumentMiddlewareMetrics(reg)

	// vertical-sharding middleware.
	if numShards > 0 {
		analyzer := querysharding.NewQueryAnalyzer()
		instantQueryMiddlewares = append(
			instantQueryMiddlewares,
			queryrange.InstrumentMiddleware("sharding", m),
			PromQLShardingMiddleware(analyzer, numShards, limits, codec, reg),
		)
	}

	// stats middleware.
	instantQueryMiddlewares = append(
		instantQueryMiddlewares,
		queryrange.NewStatsMiddleware(forceStats),
	)

	return func(next http.RoundTripper) http.RoundTripper {
		// QueryTripper
		rt := queryrange.NewRoundTripper(next, codec, forwardHeaders, instantQueryMiddlewares...)
		return queryrange.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return rt.RoundTrip(r)
		})
	}
}

// shouldCache 判断是否应该缓存响应数据.
// 响应数据不会被缓存的情况:
// 1) dedup=false;
// 2) 指定了 Store Matchers 的请求;
// 3) 下游对于请求的响应头中设置了 Cache-Control=no-store; 当下游出现部分响应或响应中出现警告时.
func shouldCache(r queryrange.Request) bool {
	if thanosReqStoreMatcherGettable, ok := r.(ThanosRequestStoreMatcherGetter); ok {
		if len(thanosReqStoreMatcherGettable.GetStoreMatchers()) > 0 {
			return false
		}
	}

	if thanosReqDedup, ok := r.(ThanosRequestDedup); ok {
		if !thanosReqDedup.IsDedupEnabled() {
			return false
		}
	}

	return !r.GetCachingOptions().Disabled
}
