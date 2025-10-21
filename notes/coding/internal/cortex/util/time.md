## 函数调用关系


### 其他
### 1. time.Parse
time.RFC3339Nano >= time.RFC3339. 意思是 RFC3339Nano 既能解析Nano又能解析RFC3339. 至少在 go1.24中该行为表现正确.  

但是, ChatGPT 说是 1.21 之后, Go 解析 time.RFC3339Nano 时支持小数部分缺省.

### 2. func math.Modf(f float64) (int float64, frac float64)
该函数返回浮点数证书部分与小数部分.

### 3. func math.Round(x float64) float64
函数返回参数四舍五入后的整数.

### 4. func time.Unix(sec int64, nsec int64) time.Time
sec: 从 1970年1月1日 UTC 起的秒数
nsec: 纳秒偏移(在 sec 的基础上加多少纳秒)
返回值是一个 time.Time 类型，表示对应的本地时间
秒 = 1000 毫秒(millisecond) = 1000000 微秒(microsecond) = 1000000000 纳秒(nanosecond)


### 5. Unix 时间戳
| 格式类型 | 标准依据 | 说明 | 示例 |
| - | - | - | - |
| Unix 时间戳(整数) | POSIX/Unix Epoch | 精度为秒 | 1715493600 |
| Unix 时间戳(小数) | ISO 8601 扩展格式 | 精度为亚秒(毫秒/微妙), 绝大多数业界将小数作为毫秒使用. | 1715490600.123 |