## http.Transport
http.Transport 是 Go 中 http.Client 底层用于执行请求的结构体. 它负责实际的 TCP 连接管理, KeepAlive, TLS 握手, DNS 解析等.

```go
http.Transport{
    Proxy: http.ProxyFromEnvironment,
    DialContext: (&net.Dialer{
        Timeout:   30 * time.Second,
        KeepAlive: 30 * time.Second,
        DualStack: true,
    }).DialContext,
    ForceAttemptHTTP2:     true, // 如果目标服务器支持 HTTP/2（通过 ALPN 协商或 h2c），那么即使你没显式启用 HTTP/2，也强制尝试使用 HTTP/2
    MaxIdleConns:          100,
    IdleConnTimeout:       90 * time.Second,
    TLSHandshakeTimeout:   10 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
}
```

### http.Transport.Proxy
`Proxy` 告诉 `Transport` "你这个 HTTP 请求要不要走代理? 如果走代理, 代理地址是什么?"

函数 `http.ProxyFromEnvironment`: 自动从当前操作系统的环境变量中读取代理配置, 如 `HTTP_PROXY`, `HTTPS_PROXY` 等 

#### 为什么会存在代理这种方式?
代理是一种 "中间人" 机制, 用来增强网络访问的灵活性、安全性、控制力和隐私保护. 代理服务器代表客户端去访问目标服务器, 而不是客户端直接访问服务器.

这种方式可以实现:
- 网络隔离
- 匿名访问
- 访问控制
- 缓存加速
- 协议转换/加密网关
- 负载均衡/高可用
- 跨地域访问/绕过封锁

### http.Transport.DailContext
DailContext 控制 HTTP 请求底层如何建立 TCP 连接. 默认的 `http.Transport` 会使用 `net.Dailer{}` 来创建连接, 但是如果使用者希望自定义控制行为, 需要配置: 

- 连接超时
- TCP KeepAlive 行为
- 支持 IPv6 与 IPv4 自动切换

```go
&net.Dialer{
    Timeout:   30 * time.Second, // 建立 TCP 连接的最大时间，超过就报错, http.Client.Timeout 控制的是总的超时时间, 包括TCP 建连、发送请求、服务器处理、读取响应体.
    KeepAlive: 30 * time.Second, // TCP 层的 keepalive，适用于长连接（连接复用），系统会定期发送探测包，保持连接活跃
    DualStack: true,             // 支持同时使用 IPv4 和 IPv6（Happy Eyeballs）
}
```

### http.Transport.ExpectContinueTimeout
该字段与 HTTP/1.1 的 `Except: 100-continue` 有关. 当请求头中包含 `Except: 100-continue` 时, 客户端会等服务器确认再发送请求体, 这个字段定义了客户端等待服务端响应 100 continue 的最大时间.


在某些场景下（尤其是请求体很大，比如上传文件时），我们希望先只发 header，让服务端看看 header 是不是合法（比如鉴权、URL 是否允许），服务端返回 100 Continue 后再继续上传 body.

### GPT 解释
```go
resp, err := http.Get("http://example.com")
```
内部实际执行流程：

http.Get 创建一个 http.Request

http.Client.Do(request) 被调用

http.Client 调用它的 Transport.RoundTrip(request)

http.Transport.RoundTrip 做了这些事：

✅ 拿连接（或复用）
✅ 做 DNS 解析
✅ 建立 TCP / TLS 连接
✅ 写 HTTP 请求（包括 header + body）
✅ 读取 HTTP 响应（状态行 + header + body）
✅ 返回 http.Response 给 Client

### ⚠️注意注意
1. 如果 observability 可以观察到 Transport 中 TCP 连接释放/建立等情况, 就可以更好的配置 Transport.


## http.ServeMux
在 Go 的标准库 http.ServeMux 中，路由匹配采用 最长前缀匹配（Longest Prefix Match） 规则.

```go
mux.Handle("/", test)       // fallback handler，匹配所有路径
mux.Handle("/api", api)     // 精确匹配或作为前缀匹配
```

当请求 /api/v1/test 到来时：
ServeMux 会检查所有已注册的路径前缀（/, /api）

找出最长的、能匹配请求路径开头的那个 pattern

"/api" 是最长匹配前缀（比 "/" 更长）

所以会选中 mux.Handle("/api", api) 对应的 handler
