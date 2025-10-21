## 1. HTTP 请求参数形式
1. application/x-www-form-urlencoded
   
   `Content-Type: application/x-www-form-urlencoded`

   参数放在请求体中, 形式为 key=value&key2=value2

   该方式是浏览器表单默认的提交方式
2. query parameters
   
   参数写在 URL 的 `?` 后面, 以 key=value&key2=value2 格式

3. multipart/form-data
   
   `Content-Type: mulipart/form-data`

   使用边界分割(boundary)方式分段表示每个字段

   ```http
   POST /upload HTTP/1.1
   Content-Type: multipart/form-data; boundary=----WebKitFormBoundaryXYZ
   
   ------WebKitFormBoundaryXYZ
   Content-Disposition: form-data; name="description"
   
   A file upload
   ------WebKitFormBoundaryXYZ
   Content-Disposition: form-data; name="file"; filename="example.txt"
   Content-Type: text/plain

   (file content here)
   ------WebKitFormBoundaryXYZ--
   ```

   多用于文件, 富文本上传

`Content-Type` 是 HTTP Header 中的一个字段, 表示消息体(body)中的数据格式.

## 2. func strings.EqualFold(s string, t string) bool
是 Go 标准库中用于比较两个字符串是否在忽略大小写的前提下相等. 之所以用 Fold 这个单词, 是因为在 Unicode 中, case folding 表示标准化过程, 将所有大小写字母转换为统一的形式.

## 3. url.Values
通常用于 query parameters 和 form data.

## 4. strconv.Format<类型>
将<类型>以 string 方式返回

## 5. func io.NopCloser(r io.Reader) io.ReadCloser
该函数将 io.Reader 封装成 io.ReaderCloser, Close方法什么都不做. 这种转换要确保 io.Reader 是不需要关闭的.
比如 `bytes.Buffer`, `strings.Reader` 本身就没有资源释放, 就不需要Close方法做什么.

## 6. 为什么 Instant Query 的数据返回类型会有 matrix?
因为 Instant Query 支持 `cpu_usage_idle{source="monitor", host="hpc-inside-2"}[1h]`, 这种情况下返回的就是 matrix 类型的数据.