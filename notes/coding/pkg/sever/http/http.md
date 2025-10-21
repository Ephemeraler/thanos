## http.Server V.S. http.ServerMux
### http.Server
`http.Server` 表示一个 HTTP 服务器, 包括端口监听、请求处理、TLS、超时控制等.

### http.ServerMux
`http.ServerMux` 是 Golang 标准库中默认的 HTTP 路由器(Multiplexer), 负责将不同 URL 请求分发到对应的处理函数(handler).


## Exporter 基于 http.Server
```go
// 创建 prometheus.Registry(reg)
reg := prometheus.NewRegistry()

// 需要注意的是 prometheus 包中有默认 Registerer
// 设置 prometheus 包默认 Registerer
prometheus.DefaultRegisterer = reg

// 使用 http.Server 暴露(增加 /metrics handler)
mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		}))
```

## observability
可观察性（Observability）是指能够从系统的外部输出推断出其内部状态的能力.

## 不理解
1. http.Server 启动使用了 toolkit_web 进行了封装, 不知道该封装的意义.

## http.Handler
http.Handler 是一个接口, 表示 http 请求处理器. ServerHTTP 是请求处理的入口.
```go
type Handler interface {
    ServeHTTP(ResponseWriter, *Request)
}
```

http.HandlerFunc 是 Go 标准库中的一个类型适配器（type adapter），它的作用是: 允许你用一个普通的函数，来实现 http.Handler 接口.
http.HandlerFunc 也是特别有意思, 其实现 ServeHTTP 也是调用函数本身.


http.HandleFunc 是快捷函数, 用于将 handler 注册到 http.DefaultServeMux.