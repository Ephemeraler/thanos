// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package frontend

import (
	"net/http"
	"net/url"
	"path"

	"github.com/opentracing/opentracing-go"
)

// downstreamRoundTripper 实现 http.RoundTripper 接口, 处理请求 URL 重定向到 downstream.url. 并由 http.Transport.RoundTrip 执行请求.
type downstreamRoundTripper struct {
	downstreamURL *url.URL
	transport     http.RoundTripper // 本质为 http.Transport
}

// NewDownstreamRoundTripper 创建 downstreamRoundTripper.
func NewDownstreamRoundTripper(downstreamURL string, transport http.RoundTripper) (http.RoundTripper, error) {
	// 解析 downstram URL.
	u, err := url.Parse(downstreamURL)
	if err != nil {
		return nil, err
	}

	return &downstreamRoundTripper{downstreamURL: u, transport: transport}, nil
}

// RoundTrip 增加 opentracing 的支持, 将请求转发到 downstream URL, 并执行 next.
func (d downstreamRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	tracer, span := opentracing.GlobalTracer(), opentracing.SpanFromContext(r.Context())
	if tracer != nil && span != nil {
		carrier := opentracing.HTTPHeadersCarrier(r.Header)
		err := tracer.Inject(span.Context(), opentracing.HTTPHeaders, carrier)
		if err != nil {
			return nil, err
		}
	}

	// 将原始请求 "重定向" 到 downstream url.
	r.URL.Scheme = d.downstreamURL.Scheme
	r.URL.Host = d.downstreamURL.Host
	r.URL.Path = path.Join(d.downstreamURL.Path, r.URL.Path)
	// 这里需要将原始请求的 Host 设置为空, 否则会导致请求失败.
	// 这是为了让 http.Transport 在发送请求时，自动根据 r.URL.Host 来设置 Host 头，而不是使用原请求中的 r.Host 值
	r.Host = ""

	// 执行 next.
	return d.transport.RoundTrip(r)
}
