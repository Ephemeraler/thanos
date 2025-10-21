## downstreamRoundTripper.RoundTripper 为什么讲 r.Host 置空?
downstreamRoundTripper.RoundTripper 核心是修改请求, 将请求转发到 downstram url 上. 若 host 不置空会出现下述情况:

接收到请求
```http
GET /api/v1/hello HTTP/1.1
Host: original.com
```

若不清空, 请求发送给下游时会变成
```http
GET /api/v1/hello_a HTTP/1.1
Host: original.com  ❌（错的！和 backend 无关）
```

如果清空, Go 会自动设置 Host.