## 一些思考
### 命名标准
1. options 是 struct 包含很多选项, 因此命名为 options. 在多个组件共享使用这个结构体时, options 中的众多选项并非每个组件都会使用到, 因此为了保持扩展性使用选项模式配置.
2. Option 是 interface 表示具体配置某一个选项, 因此命名为 Option.
3. optionFunc 是 Option 的具体实现.


### 为什么不在 With*** 中直接传递 *options
因为在调用 With*** 时, *options 可能还未创建. 如
```go
srv := httpserver.New(logger, reg, comp, httpProbe,
			httpserver.WithListen(cfg.http.bindAddress),
			httpserver.WithGracePeriod(time.Duration(cfg.http.gracePeriod)),
			httpserver.WithTLSConfig(cfg.http.tlsConfig),
		)
```
一般配置在配置模块或命名行解析之后就会读取到, 而 option 的创建其实是在 httpserver 的内部.

### optionFunc 与 apply
其实是 apply 接收到的参数传递给 optionFunc, 这种编程手法是利用成员函数获取函数类型执行时所需要的参数.