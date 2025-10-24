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

8. LimitsMiddleware
   1. LabelsTripper, QueryRangeTripper 使用该层, 参数与命令行对应设置已在 internal/cortex/querier/queryrange/limit.go:Limit接口中注释
   2. 当前我在看的这个版本 LimitsMiddleware 并没有实现 TenantLimit 功能, 但是已经预留扩展位.
   3. 根据 --query-range.max-query-length 检查 end - start 查询的时间范围长度是否超过设置的默认值. 默认值 0. 0 表示没有限制, 否则判断 end - time 是否超过限制, 若是则直接返回, 不会再继续下层请求处理..
   4. Do
      该函数对 LookBack 和 QueryLength 的限制进行判断. 超过限制的则直接返回或修改部分请求参数. 值得注意的是 Thanos 当前并未使用 LookBack 限制, 并且该 Lookback 与 prometheu 的 lookback 意义完全不同. Thanos LookBack 是指逻辑上该 Frontend 持久的数据中 "最旧" 的时间点, 超过该时间的查询要么直接返回(End < Lookback), 要么将请求参数修改(start < Lookback 时, start = now - lookback). 而 Prometheus Lookback 是在表达式执行阶段, 每个时间点能够寻找的样本范围. QueryLength 超限则直接返回.

9. StatsMiddleware(先放弃)
    1. frontend 中 --query-frontend.force-query-stats(默认 false)参数影响该层.

    遗留的问题: 
    1. 为什么 pkg/queryfrontend/request.go:ThanosQueryRangeRequest 的 With 函数都是在拷贝 ThanosQueryRangeRequest 中设置值并返回拷贝请求呢?
    2. pkg/queryfrontend/request.go:ThanosQueryRangeRequest 中 Stats 变量的作用?
    3. 响应中的 Stats 变量又是干什么的呢?

10. StepAlignMiddleware
    由命令行参数 --query-range.align-range-with-step 控制. 默认为 true. 该层主要是调整start, end参数值, 使得 start, end 向下偏移到能够被 step 整除的时间点.

    遗留问题: 对齐的好处有哪些?

11. DownsampledMiddlewar
    1. 降采样层什么条件下开启，什么条件下关闭？
       首先, frontend 发送的请求中就没有关于降采样开启/关闭的参数, 只有 `max_source_resolution`, 通过该参数才会决定降采样层会不会开启. 当 `max_source_resolution` == `auto` 时, 会开启降采样(置ThanosQueryRangeRequest.AutoSampling = true), 同时会将ThanosQueryRangeRequest.MaxSourceResolution 设置为 step / 5.   

    2. 降采样层为什么需要额外请求？因为数据当前分辨率请求获取的数据不完整.
    3. metric:thanos_frontend_downsampled_extra_queries_total 表示将采样需要额外的请求
    4. 我现在唯一不明白的一点就是为什么选择 step / 5? 
       是 Thanos 官方的一个经验魔数. GPT 给出了一个解释"你的面板每 step 秒画一个点；让数据源分辨率 ≤ step/5，基本能保证每个点位附近总有样本可用（减少对齐抖动、陈旧窗口等带来的“取不到值"

  整理 max_source_resolution 的参数对 DownsamplingMiddleware的作用
  - 非 auto
    max_source_resolution != "auto" 时，这个 Middleware 直接放行到 next，不会改写分辨率、也不会做任何重试。

  - auto 路径
    先把 max_source_resolution 设为 step/5
    用这个分辨率发起一次查询。
    检查结果是否“覆盖完整时间范围”（按步长对齐后看是否存在“预期时间戳缺失/空洞”之类的判定）。如果不完整：
      把 max_source_resolution 提升到比当前更大的“下一个默认档位”（如 raw→5m、5m→1h），
      重新发起请求并合并结果；
      循环，直到“时间范围被完整覆盖”或“已无更高默认档位可用”

### 问题
1. pkg/queryfrontend/request.go:ThanosLabelsRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
2. pkg/queryfrontend/request.go:ThanosSeriesRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
3. pkg/queryfrontend/request.go:ThanosQueryInstantRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
4. pkg/queryfrontend/request.go:ThanosQueryRangeRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
5. pkg/queryfrontend/request.go: 为什么 With 函数都是 clone 份 request 再设置后返回拷贝的 request.