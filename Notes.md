1. Tripperware
   - TenancyTripper (pkg/tenancy/tenancy.go): 对租户信息统一处理
     - RouterTripper (pkg/queryfrontend/roundtrip.go): 路由请求到对应的下层 Tripperware.
       - LabelsTripper(pkg/queryfrontend/roundtrip.go): 到这一层即是 http.RoundTripper 又是 Handler.
         - DownstreamTripper
           - http.Transport
       - QueryInstantTripper(pkg/queryfrontend/roundtrip.go):
         注册指标 cortex_frontend_query_range_duration_seconds
         到这一层即是 http.RoundTripper 又是 Handler, RoundTrip 内部调用 Do.
         - DownstreamTripper
           - http.Transport
       - QueryRangeTripper(pkg/queryfront/roundtrip.go): 到这一层即是 http.RoundTripper 又是 Handler.
         - DownstramTripper
           - http.Transport
       - DownstreamTripper
         - http.Transport


2. 在仔细说一下 Tripperware 和 Handler 之间的关系. 到了 LabelsTripper、QueryInstantTripper、QueryRangeTripper 这一层的 http.RoundTripper 后, 其即实现了http.RoundTripper, 又实现了 Handler. 在其 RoundTrip 到 DownStreamTipper 之间经历了: 各种 Handlerware, 即 Do 函数 -> LabelsTripper|QueryInstantTripper|QueryRangeTripper.Do 函数 -> DownstreamTripper.RoundTrip

3. 不管 Tipperware 怎么设置, 最后一层一定是 http.Transport, 要不没法发送请求.
   
4. RoundTripper-Handler Adapter
   - Handler（客户端侧处理管线）: 使用 Thanos Request/Response.
     承担“一次逻辑请求”的完整处理：重试/退避、超时与截止、熔断/限流、幂等判断、缓存短路、观测（trace/metrics/日志）、请求编码/响应解码（经由 Codec）、以及可复用的“业务层/协议层”中间件。允许多次尝试（重试/hedging）但只对外暴露“一次逻辑请求”的语义与指标。更易测试（可用假实现、不走真实网络）、更易跨协议（不仅限 HTTP/1.1；也能换 h2c、代理、甚至非 HTTP 的后端）。
   - roundTripper（http.RoundTripper 适配层）: 使用 http.Request/Response.
     是边缘适配器：把标准 http.Client 的调用（RoundTrip(*http.Request)）接入 Handler 管线，并在末端再适配回 RoundTripper 语义。处理与 http.Client/传输层强相关的细节：底层传输（next http.RoundTripper）、线上的请求头注入（headers）、编解码衔接（codec）、与标准库契合（代理、连接复用、TLS、Keep-Alive 等都继续由 next 负责）。

5. http.Transport 底层 TCP 连接复用的使用技巧 - 读取完毕请求体并关闭. (一般情况下, 错误响应我都没有读取完毕响应体)

6. 搞清楚 LabelsTripper, QueryInstantTripper, QueryRangeTripper 下 Handler 链.
   1. LabelsTripper
      - internal/cortex/querier/queryrange/instrumentation.go:InstrumentMiddleware (--labels.split-interval != 0)
      - pkg/queryfrontend/split_by_interval.go:SplitByIntervalMiddleware (--labels.split-interval != 0)
      - internal/cortex/querier/queryrange/instrumentation.go:InstrumentMiddleware (--labels.response-cache-config != nil)
      - internal/cortex/querier/queryrange/results_cache.go:ResultsCacheMiddleware (--labels.response-cache-config != nil)
      - internal/cortex/querier/queryrange/instrumentation.go:InstrumentMiddleware (--labels.max-retries-per-request > 0)
      - internal/cortex/querier/queryrange/retry.go:RetryMiddleware (l--abels.max-retries-per-request > 0)
   2. QueryInstantTripper
      - internal/cortex/querier/queryrange/instrumentation.go:InstrumentMiddleware (--query-frontend.vertical-shards > 0)
      - pkg/queryfrontend/shards_query.go:PromQLShardingMiddleware(--query-frontend.vertical-shards > 0)
      - internal/cortex/querier/queryrange/stats_middleware.go:StatsMiddleware
   3. QueryRangeTripper
      - internal/cortex/querier/queryrange/limit.go:LimitsMiddleware
      - internal/cortex/querier/queryrange/stats_middleware.go:StatsMiddleware
      - internal/cortex/querier/queryrange/instrumentation.go: InstrumentMiddleware(--query-range.align-range-with-step == true)
      - internal/cortex/querier/queryrange/step_algin.go:StepAlignMiddleware(--query-range.align-range-with-step == true)
      - internal/cortex/querier/queryrange/instrumentation.go: InstrumentMiddleware(--query-range.request-downsampled == true)
      - pkg/queryfrontend/downsampled.go:DownsampledMiddleware(--query-range.request-downsampled == true)
      - internal/cortex/querier/queryrange/instrumentation.go: InstrumentMiddleware(--query-range.split-interval != 0 或 --query-range.min-split-interval != 0)
      - pkg/queryfrontend/split_by_interval.go:SplitByIntervalMiddleware(--query-range.split-interval != 0 或 --query-range.min-split-interval != 0)
      - pkg/queryfrontend/shards_query.go:PromQLShardingMiddleware(--query-frontend.vertical-shards > 0)
      - internal/cortex/querier/queryrange/instrumentation.go:InstrumentMiddleware (--query-range.response-cache-config !=nil )
      - internal/cortex/querier/queryrange/results_cache.go:ResultsCacheMiddleware (--query-range.response-cache-config !=nil )
      - internal/cortex/querier/queryrange/instrumentation.go:InstrumentMiddleware (--query-range.max-retries-per-request > 0)
      - internal/cortex/querier/queryrange/retry.go:RetryMiddleware (--query-range.max-retries-per-request > 0)

7. InstrumentMiddleware(InstrumentHandler)
   该函数用于记录 next.Do 的执行时间指标 cortext_frontend_query_range_duration_seconds{method="", status_code="", tripperware=""}, 这里需要注意 metrics 中的 query_range 不是特指 Prometheus QueryRange 查询, 而是表示范围类查询, 因此包括 /api/v1/labels, /api/v1/label/.+/values, /api/v1/series 的查询. 在 thanos frontend 中 tripperware 共包括 labels, query, query_range 类别.
8. 

### 问题
1. pkg/queryfrontend/request.go:ThanosLabelsRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
2. pkg/queryfrontend/request.go:ThanosSeriesRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
3. pkg/queryfrontend/request.go:ThanosQueryInstantRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
4. pkg/queryfrontend/request.go:ThanosQueryRangeRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?