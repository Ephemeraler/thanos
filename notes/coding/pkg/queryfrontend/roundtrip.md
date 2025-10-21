## 函数调用关系图
- NewTripperware (pkg/queryfrontend/roundtrip.go)
  - validation.NewOverrides (internal/cortex/util/validation/limits.go)
  - NewThanosQueryRangeCodec (pkg/queryfrontend/queryrange_code.go)
  - NewThanosLabelsCodec (pkg/queryfrontend/labels_codec.go)
  - NewThanosQueryInstantCodec (pkg/queryfrontend/queryinstant_codec.go)
  - newQueryRangeTripperware (pkg/queryfrontend/roundtrip.go)
    - dynamicIntervalFn
    - roundTripper.RoundTrip
      - getOperation
    - shouldCache
  - newLabelsTripperware
    - roundTripper.RoundTrip
      - getOperation
    - shouldCache
  - newInstantQueryTripperware
    - roundTripper.RoundTrip
      - getOperation
  - newRoundTripper

## 其他
### 1. Tripperware
非正式的、社区约定俗称的术语, 通常在使用 HTTP Client 的中间件时出现. `Tripperware` 是 `RoundTripper` 和 `middleware` 的组合词. 通常指包装 `http.RoundTripper` 的中间件函数.

在 Golang 的 `http.Client` 中, 发送逻辑是由 `Transport` 实现的, 其接口为:
```go
type RoundTripper interface {
    RoundTrip(*http.Request) (*http.Response, error)
}
```

如果想在发送过程前后增加自定义逻辑, 可以包装 `Transport`.

示例:
```go
type LoggingRoundTripper struct {
    next http.RoundTripper
}

func (lrt *LoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    fmt.Println("Request:", req.Method, req.URL)
    return lrt.next.RoundTrip(req)
}

func WrapWithLogging(rt http.RoundTripper) http.RoundTripper {
    return &LoggingRoundTripper{next: rt}
}

client := &http.Client{
    Transport: WrapWithLogging(http.DefaultTransport)
}
```

常见用途: 
- 添加 trace ID;
- 注入 JWT token;
- 日志记录
- 重试逻辑
- 指定代理或连接方式
- Metrics

### 2. codec
一般指 "coder-decoder" 缩写, 广泛用于数据序列化/反序列化领域.

### 3. strings.HasSuffix
检查后缀石是否匹配.