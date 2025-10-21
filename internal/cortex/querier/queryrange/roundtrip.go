// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

// Copyright 2016 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Mostly lifted from prometheus/web/api/v1/api.go.

package queryrange

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/prometheus/promql"
	"github.com/weaveworks/common/httpgrpc"
	"github.com/weaveworks/common/user"

	"github.com/thanos-io/thanos/internal/cortex/chunk/cache"
	"github.com/thanos-io/thanos/internal/cortex/querier"
	"github.com/thanos-io/thanos/internal/cortex/tenant"
	"github.com/thanos-io/thanos/internal/cortex/util"
	"github.com/thanos-io/thanos/internal/cortex/util/flagext"
)

const day = 24 * time.Hour

var (
	// PassthroughMiddleware is a noop middleware
	PassthroughMiddleware = MiddlewareFunc(func(next Handler) Handler {
		return next
	})

	errInvalidMinShardingLookback = errors.New("a non-zero value is required for querier.query-ingesters-within when -querier.parallelise-shardable-queries is enabled")
)

// Config for query_range middleware chain.
type Config struct {
	SplitQueriesByInterval time.Duration `yaml:"split_queries_by_interval"`
	AlignQueriesWithStep   bool          `yaml:"align_queries_with_step"`
	ResultsCacheConfig     `yaml:"results_cache"`
	CacheResults           bool `yaml:"cache_results"`
	MaxRetries             int  `yaml:"max_retries"`
	ShardedQueries         bool `yaml:"parallelise_shardable_queries"`
	// List of headers which query_range middleware chain would forward to downstream querier.
	ForwardHeaders flagext.StringSlice `yaml:"forward_headers_list"`
}

// Validate validates the config.
func (cfg *Config) Validate(qCfg querier.Config) error {
	if cfg.CacheResults {
		if cfg.SplitQueriesByInterval <= 0 {
			return errors.New("querier.cache-results may only be enabled in conjunction with querier.split-queries-by-interval. Please set the latter")
		}
		if err := cfg.ResultsCacheConfig.Validate(qCfg); err != nil {
			return errors.Wrap(err, "invalid ResultsCache config")
		}
	}
	return nil
}

// HandlerFunc Handler 接口的函数适配器.
type HandlerFunc func(context.Context, Request) (Response, error)

// Do implements Handler.
func (q HandlerFunc) Do(ctx context.Context, req Request) (Response, error) {
	return q(ctx, req)
}

// Handler is like http.Handle, but specifically for Prometheus query_range calls.
type Handler interface {
	Do(context.Context, Request) (Response, error)
}

// MiddlewareFunc Middleware 接口类型的函数适配器.
type MiddlewareFunc func(Handler) Handler

func (q MiddlewareFunc) Wrap(h Handler) Handler {
	return q(h)
}

// Middleware 是 Handler 中间层封装器.
type Middleware interface {
	Wrap(Handler) Handler
}

// MergeMiddlewares 返回的是 Middleware. 根据 middlerware 倒叙顺序逐层在 next 外层封装.
// 其在实际中的执行顺序是 middleware 中的添加顺序, 最后执行 next
func MergeMiddlewares(middleware ...Middleware) Middleware {
	return MiddlewareFunc(func(next Handler) Handler {
		// middleware 倒叙遍历. 也是倒叙封装
		for i := len(middleware) - 1; i >= 0; i-- {
			next = middleware[i].Wrap(next)
		}
		return next
	})
}

// Tripperware 是所有 http 客户端侧中间层的签名
type Tripperware func(http.RoundTripper) http.RoundTripper

// RoundTripFunc http.RoundTripper 接口的函数适配器.
type RoundTripFunc func(*http.Request) (*http.Response, error)

func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// NewTripperware returns a Tripperware configured with middlewares to limit, align, split, retry and cache requests.
func NewTripperware(
	cfg Config,
	log log.Logger,
	limits Limits,
	codec Codec,
	cacheExtractor Extractor,
	engineOpts promql.EngineOpts,
	minShardingLookback time.Duration,
	registerer prometheus.Registerer,
	cacheGenNumberLoader CacheGenNumberLoader,
) (Tripperware, cache.Cache, error) {
	// Per tenant query metrics.
	queriesPerTenant := promauto.With(registerer).NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_query_frontend_queries_total",
		Help: "Total queries sent per tenant.",
	}, []string{"op", "user"})

	activeUsers := util.NewActiveUsersCleanupWithDefaultValues(func(user string) {
		err := util.DeleteMatchingLabels(queriesPerTenant, map[string]string{"user": user})
		if err != nil {
			level.Warn(log).Log("msg", "failed to remove cortex_query_frontend_queries_total metric for user", "user", user)
		}
	})

	// Metric used to keep track of each middleware execution duration.
	metrics := NewInstrumentMiddlewareMetrics(registerer)

	queryRangeMiddleware := []Middleware{NewLimitsMiddleware(limits)}
	if cfg.AlignQueriesWithStep {
		queryRangeMiddleware = append(queryRangeMiddleware, InstrumentMiddleware("step_align", metrics), StepAlignMiddleware)
	}
	if cfg.SplitQueriesByInterval != 0 {
		staticIntervalFn := func(_ Request) time.Duration { return cfg.SplitQueriesByInterval }
		queryRangeMiddleware = append(queryRangeMiddleware, InstrumentMiddleware("split_by_interval", metrics), SplitByIntervalMiddleware(staticIntervalFn, limits, codec, registerer))
	}

	var c cache.Cache
	if cfg.CacheResults {
		shouldCache := func(r Request) bool {
			return !r.GetCachingOptions().Disabled
		}
		queryCacheMiddleware, cache, err := NewResultsCacheMiddleware(log, cfg.ResultsCacheConfig, constSplitter(cfg.SplitQueriesByInterval), limits, codec, cacheExtractor, cacheGenNumberLoader, shouldCache, registerer)
		if err != nil {
			return nil, nil, err
		}
		c = cache
		queryRangeMiddleware = append(queryRangeMiddleware, InstrumentMiddleware("results_cache", metrics), queryCacheMiddleware)
	}

	if cfg.MaxRetries > 0 {
		queryRangeMiddleware = append(queryRangeMiddleware, InstrumentMiddleware("retry", metrics), NewRetryMiddleware(log, cfg.MaxRetries, NewRetryMiddlewareMetrics(registerer)))
	}

	// Start cleanup. If cleaner stops or fail, we will simply not clean the metrics for inactive users.
	_ = activeUsers.StartAsync(context.Background())
	return func(next http.RoundTripper) http.RoundTripper {
		// Finally, if the user selected any query range middleware, stitch it in.
		if len(queryRangeMiddleware) > 0 {
			queryrange := NewRoundTripper(next, codec, cfg.ForwardHeaders, queryRangeMiddleware...)
			return RoundTripFunc(func(r *http.Request) (*http.Response, error) {
				isQueryRange := strings.HasSuffix(r.URL.Path, "/query_range")
				op := "query"
				if isQueryRange {
					op = "query_range"
				}

				tenantIDs, err := tenant.TenantIDs(r.Context())
				// This should never happen anyways because we have auth middleware before this.
				if err != nil {
					return nil, err
				}
				userStr := tenant.JoinTenantIDs(tenantIDs)
				activeUsers.UpdateUserTimestamp(userStr, time.Now())
				queriesPerTenant.WithLabelValues(op, userStr).Inc()

				if !isQueryRange {
					return next.RoundTrip(r)
				}
				return queryrange.RoundTrip(r)
			})
		}
		return next
	}, c, nil
}

// roundTripper 该类型为 RoundTripper-Handler Adapter.
// 由 RoundTrip 函数, 由 http.RoundTripper 进入 Handler, 再由 Do 回到 http.RoundTripper.
type roundTripper struct {
	next    http.RoundTripper
	handler Handler
	codec   Codec
	headers []string
}

// NewRoundTripper 创建 RoundTripper-Handler Adapter, 并基于参数 Middleware 注入 Handler 链.
func NewRoundTripper(next http.RoundTripper, codec Codec, headers []string, middlewares ...Middleware) http.RoundTripper {
	transport := roundTripper{
		next:    next,
		codec:   codec,
		headers: headers,
	}

	// 这里就是
	transport.handler = MergeMiddlewares(middlewares...).Wrap(&transport)
	return transport
}

// RoundTrip 核心功能 http.Request 编码为 Request -> Handler.Do -> 解码 Response 到 http.Response
func (q roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	// 将 http.Request 编码为 Request.
	request, err := q.codec.DecodeRequest(r.Context(), r, q.headers)
	if err != nil {
		return nil, err
	}

	if span := opentracing.SpanFromContext(r.Context()); span != nil {
		request.LogToSpan(span)
	}

	// 调用 handler 执行实际请求.
	response, err := q.handler.Do(r.Context(), request)
	if err != nil {
		return nil, err
	}

	// 解码 Response 到 http.Response.
	return q.codec.EncodeResponse(r.Context(), response)
}

// Do 核心功能 Request 编码为 http.Request -> http.RoundTripper.RoundTrip -> 解码 http.Response 为 Response
func (q roundTripper) Do(ctx context.Context, r Request) (Response, error) {
	request, err := q.codec.EncodeRequest(ctx, r)
	if err != nil {
		return nil, err
	}

	if err := user.InjectOrgIDIntoHTTPRequest(ctx, request); err != nil {
		return nil, httpgrpc.Errorf(http.StatusBadRequest, "%s", err.Error())
	}

	response, err := q.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 仅关闭响应体不够安全.
		// Go 的 http.Transport 内部有一个连接复用的机制(Keep-Alive), 如果读取完 Body 并正常关闭, 就会把底层 TCP 连接"复用"给下一个请求.
		// 但是, 如果未读取完 Body 就关闭 Close, Transport 认为这次连接状态"未知", 不会复用, 甚至直接丢弃.
		// io.Copy(io.Discard, io.LimitReader(response.Body, 1024)) 确实不能完全读取完毕请求体, 但是这是一个折中的方案.
		// 1. 错误响应时, 一般返回的信息都很短, 1KB 足够读取完并复用.
		// 2. 正确响应时, 一般会读取完请求体内容. 如果没有读取完, 我们再读取 1KB 的数据还没有读取完是避免长时间阻塞或浪费带宽
		//
		// 读 1024 是一种“尽力清理但不保证可复用”的优化策略.
		io.Copy(io.Discard, io.LimitReader(response.Body, 1024))

		_ = response.Body.Close()
	}()

	return q.codec.DecodeResponse(ctx, response, r)
}
