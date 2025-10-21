## 函数层级调用关系
- main(cmd/thanos/main.go)
  - registerQueryFrontend(cmd/thanos/query_frontend.go)
    - runQueryFrontend(cmd/thanos/query_frontend.go)
      - NewTripperware(pkg/queryfrontend): 创建 Tripper 封装函数
      - parseTransportConfiguration: 解析 Transport 配置并创建原始 Tripper(http.Transport)
      - NewDownstreamRoundTripper(internal/cortex/frontend): 封装原始 Tripper, 生成 downstream tripper, 转发请求到 downstream url
      - tripperWare(roundTripper): 封装 downstream tripper.
      - NewHandler(internal/cortex/frontend/transport): 创建请求处理函数(http.handler)
      - New(pkg/server/http): 创建 http.Server
      - 注册路由、停止、启动 http server

## 核心处理函数调用顺序
- Handler.ServeHTTP(internal/cortex/frontend/transport/handler.go)
  - roundTripperFunc.RoundTrip(pkg/tenancy/tenancy.go)
    - roundTripper.RoundTrip(pkg/queryfrontend/roundtrip.go)
      根据请求类型调用下述某一个RoundTrip.
      - QueryRange - roundTripper.RoundTrip(internal/cortex/querier/queryrange/roundtrip.go)
      - QueryInstant - roundTripper.RoundTrip(internal/cortex/querier/queryrange/roundtrip.go)
      - Labels - roundTripper.RoundTrip(internal/cortex/querier/queryrange/roundtrip.go)
      - downstreamRoundTripper.RoundTrip(internal/cortex/frontend/downstream_roundtripper.go)
        - http.Transport.RoundTrip

## 随笔
confg.DownstreamURL 对应 query-frontend.downstream-url(URL of downstream Prometheus Query compatible API.)

## 疑问待解决
1. API 中 stats 参数的作用?
2. API 中请求头 Cache-Control:no-store 作用?