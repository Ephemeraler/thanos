## 函数调用关系

## 其他
### 1. 空白标识符
在 Go 中, `_` 表示"我要忽略它"的变量名. 例如:
```go
// 函数中不会使用第一个参数, 但是调用时仍然需要传递忽略参数.
func FunctionName(_ paramType, otherParam int) {}

// 忽略第一个返回值
_, err := GetName()
```

### 2. func (r *http.Request) FormValue(key string) string
该函数返回请求中指定字段的第一个值, 取值优先级如下:
1. application/x-www-form-urlencoded 类型的表单请求体(仅限于 POST、PUT、PATCH 方法)
2. URL查询参数
3. multipart/form-data 类型的表单请求体
4. FormValue 会在必要时自动调用 Request.ParseMultipartForm 和 Request.ParseForm 方法, 并忽略它们返回的任何错误
如果指定的 key 不存在, 则返回空字符串.

application/x-www-form-urlencoded: 参数长的很像 URL参数形式, 但是它将参数放在请求体中.
```bash
POST /submit HTTP/1.1
Content-Type: application/x-www-form-urlencoded

username=muqali&age=30
```

URL查询参数
```http
GET /api/v1/query?start=100&end=200 HTTP/1.1
```

### 3. 格式化字符串占位符
| 格式化占位符 | 含义 | 举例 |
| - | - | - |
| %v | 值的默认格式| |
| %q | 带引号的值 | fmt.Prinf("value=%q", hello) -> value="hello"|


### 4. github.com/gogo/status
用于 Go 处理 gRPC 错误, 专为与 gogo/protobuf 生成的类型配合使用, 它提供了与官方 google.golang.org/grpc/status 包类似的功能，但针对 gogo/protobuf 进行了优化.

### 5. time.Duration
time.Duration 的本质是纳秒级别的整数.

### 6. func strings.EqualFold(s string, t string) bool
接收两个字符串 s 和 t, 返回一个 bool 值, 表示它们是否在忽略大小写的前提下相等. 是一个 Unicode-aware 的大小写不敏感字符串比较函数，适用于多语言和更复杂的字符集.

### 7. http.Header
```go
type Header map[string][]string
```
key 是唯一的(在 ASCII 编码下, 不区分大小写), 但是值可以是多个.
```http
Cookie: a=1; b=2; c=3
```

### 8. bytes.Buffer
bytes.Buffer 会自动扩容. 但是, 在创建时尽量分配足够的空间, 避免使用时频繁扩容, 导致性能下降.

### 9. Marshal V.S. Unmarshal
Marshal, 即序列化/编码, 将数据用字节序列表示;
Unmarshal, 即反序列化/解码, 将字节序列用结构化数据表示;

### 10. func io.NopCloser(r io.Reader) io.ReadCloser
NopCloser 返回一个实现了 ReadCloser 接口的对象，它封装了你提供的 Reader r, 并带有一个“无操作（no-op）”的 Close 方法.

### 11. 为什么流式读写适合大数量场景
流式指边读取/写入边处理, 不需要将整个文件一次性读入或写入到内存. 特点就是节省内存, 所以适合大数据场景.
而相对的称为"全量读写", 也是最常用的方式, 需要将数据全部读取到内存或写入到内存才能继续操作.

github.com/json-iterator/go 支持流式JSON数据处理

encoding/json 是全量读写模式

### 12. slice 下标
```go
func main() {
	a := []int{1, 2, 3, 4, 5}
	fmt.Println(len(a)) // 输出 5
	fmt.Println(a[len(a):]) // 输出 []
	fmt.Println(a[5]) // 为什么 panic? a[len(a):] 不也是 a[5:]吗？
}
```

`a[low:high]` 表示从下表 low 到 high(不含), 共 high - low 个元素. 只要满足 `0 <= low <= high <= len(a)` 就是合法的. 而low == high == len(a) 是允许的，表示一个空区间.

### 13. func sort.Search(n int, f func(int) bool) int
n: 表示搜索区间 [0, n)

f: 判断条件函数, 必须满足"一旦f(i)返回true, 后续所有 j > i 都必须也是 true".

返回值: 返回第一个满足 f(i) == true 的下标. 如果都不满足, 返回 n.