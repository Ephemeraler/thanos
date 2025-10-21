## CORS
CORS(Cross-Origin Resource Sharing) 是浏览器的同源策略限制机制的扩展, 它允许服务器跨域访问服务接口. 用于告诉浏览器
"哪些源(origin) 可以访问我?用什么方法?带什么头?"

CORS 是现代 Web 安全架构中的关键机制之一, 它的出现是为了平衡前端灵活性和后端安全性.

### 为什么会有 CORS?
问题根源: 同源策略(Same-Origin Policy)
浏览器中存在一个非常重要的安全机制: **同源策略**, 该策略规定: "一个网页只能访问与它'同源'的资源". 所谓同源, 就是指 '协议' + '域名' + '端口' 都一样.
同源策略的目的是保护用户隐私、Cookie、安全令牌等, 防止恶意网站偷数据.

而现代web通常使用前后端分离架构, 很难保证前端和后端是 '同源'的. 如:

前端部署在 `http://frontend.com`

后端部署在 `http://api.myapp.com`

这种架构就是跨域的, 也就是前后端非同源. 这种请求浏览器默认会阻止前端获取响应结果(注意: 跨域的请求是发送出去了的, 只是浏览器阻止 JS 获取响应结果). 于是, 就出现 CORS 技术, 让后端明确告诉浏览器: "我允许谁访问我".

### CORS 是怎样工作的
#### 简单请求
CORS 是一种基于 HTTP 响应头的协议规范.

前端发送请求, 比如:

```js
fetch("https://api.example.com/data")
```

该请求同时会带 Origin 头, 表示请求从哪个 "源"(Origin) 发出:

`Origin: http://frontend.com`

后端响应头中必须包含:  
`Access-Control-Allow-Origin: http://frontend.com`  

当包含了该响应头浏览器就不会阻止响应.

#### 复杂请求
如果请求中包含下述请求, 就会被认为是 "复杂请求", 会先发一个 OPTIONS 请求(称为"预检请求"):

- 自定义 HTTP 方法, 如 PUT
- 自定义头, 如 `Autorization`
- Content-Type 是 `application/json`

浏览器会发出请求:

```http
OPTIONS /data HTTP/1.1
Origin: https://frontend.com
Access-Control-Request-Method: PUT
Access-Control-Request-Headers: Authorization
```

后端需要返回:
```http
Access-Control-Allow-Origin: https://frontend.com
Access-Control-Allow-Methods: GET, POST, PUT
Access-Control-Allow-Headers: Authorization
```
### CORS Headers
| Header                             | 含义                                                 |
| ---------------------------------- | ---------------------------------------------------- |
| `Access-Control-Allow-Headers`     | 允许前端请求时带哪些自定义的头                       |
| `Access-Control-Allow-Methods`     | 允许哪些 HTTP 方法                                   |
| `Access-Control-Allow-Origin`      | 指定允许访问资源的来源域, '*' 表示任意来源域均可访问 |
| `Access-Control-Expose-Headers`    | 告诉浏览器响应中哪些头可以暴露给 JS                  |
| `Access-Control-Allow-Credentials` | 是否允许携带 Cookie (必须设置为 true)                |
| `Access-Control-Max-Age`           | 预检请求的缓存时间                                   |