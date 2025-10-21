### 组件启动函数签名
```go
type SetupFunc func(*run.Group, log.Logger, *prometheus.Registry, opentracing.Tracer, <-chan struct{}, bool) error
```

- frontend只使用了前4个参数.
- 