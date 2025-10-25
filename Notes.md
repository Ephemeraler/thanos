1. Tripperware
   - TenancyTripper (pkg/tenancy/tenancy.go): 对租户信息统一处理
     - RouterTripper (pkg/queryfrontend/roundtrip.go): 路由请求到对应的下层 Tripperware. 本层有 thanos_query_frontend_queries_total
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

12. SplitMiddleware
    1. 开启条件:
       命令行参数 --query-range.split-interval(默认24h) 不为 0.
       或者
       命令行参数 --query-range.min-split-interval(默认0) 不为 0.

       --query-range.split-interval 参数解释 Split query range requests by an interval and execute in parallel, it should be greater than 0 when query-range.response-cache-config is configured."

       --query-range.min-split-interval 参数解释 Split query range requests above this interval in query-range.horizontal-shards requests of equal range.Using this parameter is not allowed with query-range.split-interval.One should also set query-range.split-min-horizontal-shards to a value greater than 1 to enable splitting.
    2. 参数中Split相关的有好多个, split Interval 到底应该使用哪个?
       参数中涉及到的 split 参数:
         --query-range.split-interval, 默认值: 24h
         --query-range.max-split-interval, 默认值: 0
         --query-range.min-split-interval, 默认值: 0
         --query-range.horizontal-shards, 默认值: 0

       选用原理:
         当静态 Split Interval != 0 时，使用静态 Split Interval(--query-range.split-interval);
         否则, 当请求查询的范围(end - start) > Max Split Interval(--query-range.max-split-interval) 时, 使用 Max Split Interval;
         否则, 当请求查询的范围(end - start) > Min Split Interval(--query-range.min-split-interval) 时, 使用 查询范围/水平分片数(--query-range.horizontal-shard)
         否则, 使用 Min Split Interval
    
    3. 该层 Metric
       thanos_frontend_split_queries_total

    4. Do 实现过程
       - ThanosQueryRangeRequest 类型
         - 替换查询语句中的 @ start()/end(), 确保 split 之后导致 request 中 start, end 改变不影响原始查询语义;
         - 循环切分原始请求. 切分的逻辑比较复杂, 每个请求中都做了 interval 对齐等操作. 要么 start 能被 interval 整除, 要么就是 end 能被 interval整除。也就是让每一个请求能落到对齐的interval内.
       - SplitRequest 类型
         - 直接切分子请求, 切分逻辑简单就是按照 interval 直接切分. 最后一个请求需要做边界判断.

    5. 涉及到的额外知识
       1. 查询语句中 @ modifier 的作用
          可以在 instant 和 range 查询中使用, @ 后接 unix timestamp(浮点数, 小数表示subsecond)
          例如:
          `http_requesst_total @ 1609746000` 返回的就是 2021-01-04T07:40:00+00:00 时间点的值.
          `rate(http_requests_total[5m] @ 1609746000)`  返回的就是 2021-01-04T07:40:00+00:00 时间点的值

          但是值得注意的是: @ 只决定取什么时间点的数据，并不决定响应中 Sample 的时间戳. 响应中Sample的时间戳仍然是由request中的时间参数控制.

          此外，需要注意的是，在 query range 查询中, @ 后面支持两个宏函数: start()、end() 分别对应start、end参数.
       2. 

    **待确认:**
    6. --query-range.split-interval 若为 0, --query-range.max-split-interval 和 --query-range.max-slpite-interval 应该就不会为 0, 这个可能在参数校验时有检测. 否则的话 pkg/queryfrontend/roundtrip.go: dynamicIntervalFn 肯定会 panic, 因为这两个参数都是除数, 0 不能做除数.


13. PromShardingMiddleware(Vertical Sharding)
    1. 开启的条件
       --Squery-frontend.vertical-shards > 0
    2. [Vertical Query Sharding](https://thanos.io/tip/proposals-accepted/202205-vertical-query-sharding.md/)
       Thanos 中提出了查询分片算法，通过将查询执行分布到多个 Thanos Quierier 节点上来提升较大规模查询时的查询速度.
       Thanos Sharding 算法包括:
         - Vertical Sharding: 按照时间序列(series)维度对查询进行分片. 用于解决高基数指标查询.
         - Horizontal Sharding: 按照查询时间的范围维度对查询进行分片. 用于解决长时间跨度指标查询.
       vertical sharding 的主要思想就是将raw query拆分成多个不相交的 sub queries, 能够并行的处理分发到多个 querier 上, 减少单个 querier 的负载.

       当前存在的缺陷:
       由于是 series 维度进行切分, 所以得先知道有哪些 series 才能进行切分. 会导致拉取 downstream 下所有 series, 可能会导致当前内存占用过高.

       分片算法:
       每个分片请求都携带总分片数、当前分片所属分区以及在 PromQL 中识别到的分区标签给 Stores. 这样 Store 根据这些信息就可以知道自己应该只返回哪些数据.
       
       垂直分片还有很多问题, 比如虽然在 frontend/querier 看起来在并行查询. 但是, 当前的存储结构series可能存在局部性原理。不同的分片可能在同一个 block 内部, 导致不同 store 对同一个block重复读取多次. 这种问题带来的影响开销目前 thanos 官方并没有实测.
    3. 本层指标: thanos_frontend_sharding_middleware_querier_total


### 问题
1. pkg/queryfrontend/request.go:ThanosLabelsRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
2. pkg/queryfrontend/request.go:ThanosSeriesRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
3. pkg/queryfrontend/request.go:ThanosQueryInstantRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
4. pkg/queryfrontend/request.go:ThanosQueryRangeRequest 中各个参数的作用都是由谁负责实现的, 具体功能是什么?
5. pkg/queryfrontend/request.go: 为什么 With 函数都是 clone 份 request 再设置后返回拷贝的 request.