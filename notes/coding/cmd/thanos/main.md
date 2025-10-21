### debug.SetPanicOnFault(true)
**package**

runtime/debug, 标准包

**作用**

为 true 时, 当程序访问非法内存后不会立即崩溃退出(segfault), 而是像 Go 常规 panic 一样, 触发一个 runtime 层面的 panic, 从而可以通过 recover() 机制捕获.
正常情况下程序访问非法内存后, 操作系统会直接向程序发送 `SIGSEGV` 信号, 程序会立即崩溃.
该函数通常与 recover() 函数配合使用, 让致命操作转化为可控 panic.

### recover()
**作用**

recover() 只会恢复当前goroutine中的panic, 而且只能在同一个 defer 中调用才有效. 一旦 panic 被捕获, 程序不会"自动"跳回到原来的逻辑继续执行.
⚠️ 这个函数需要深入学习一下, GPT 的说法和实际表现不一样.