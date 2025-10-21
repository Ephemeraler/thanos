## func http.MaxBytesReader(w http.ResponseWriter, r io.ReadCloser, n int64) io.ReadCloser
http.MaxBytesReader 的作用是对一个 io.ReadCloser(通常是 http.Request.Body)进行包装, 强制限制客户端请求体的最大读取字节数. 一旦超过指定的字节数, 它会自动返回 HTTP 413("Request Entity Too Large")错误, 并停止继续读取数据.

w: 用于在超出限制时写入 `413 Request Entity Too Large`

r: 原始请求体

n: 最大限制字节数

## func io.TeeReader(r io.Reader, w io.Writer) io.Reader
r: 源数据读取器，数据从这里读取;

w: 目标写入器，会把从 r 读取到的数据同时写入到这里;

返回值: 返回一个新的 io.Reader, 从这个 Reader 读取数据的时候, 其实是从 r 中读取, 同时也将读取的数据写入到 w;