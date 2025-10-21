## 函数调用关系
- NewThanosQueryRangeCodec
  - queryrange.PrometheusCodec (internal/cortex/querier/queryrange)

## 其他
### 1. http.Request.Form[key] V.S. http.Request.FormValue(key)
区别在于 Form 能够返回key的所有值, FormValue只返回第一个值.
