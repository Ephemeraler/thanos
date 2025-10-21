// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package queryfrontend

import (
	"bytes"
	"context"
	"encoding/json"
	io "io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/prometheus/prometheus/model/timestamp"
	"github.com/weaveworks/common/httpgrpc"

	"github.com/thanos-io/thanos/internal/cortex/querier/queryrange"
	cortexutil "github.com/thanos-io/thanos/internal/cortex/util"
	"github.com/thanos-io/thanos/internal/cortex/util/spanlogger"
	queryv1 "github.com/thanos-io/thanos/pkg/api/query"
	"github.com/thanos-io/thanos/pkg/store/labelpb"
)

var (
	infMinTime = time.Unix(math.MinInt64/1000+62135596801, 0)
	infMaxTime = time.Unix(math.MaxInt64/1000-62135596801, 999999999)
)

// labelsCodec Label 请求响应编解码器, 用于 ThanosLabelsRequest, ThanosSeriesRequest, ThanosLabelsResponse, ThanosSeriesResponse 与 http.Request/http.Response 之间进行编解码.
// Label 请求包括 /api/v1/labels, /api/v1/label/.+/values, /api/v1/series.
type labelsCodec struct {
	partialResponse          bool          // 默认为 true, 对应名行参数 labels.partial-response
	defaultMetadataTimeRange time.Duration // 默认 24h, 对应命令行参数 labels.default-time-range
}

// NewThanosLabelsCodec 创建 labelsCodec.
func NewThanosLabelsCodec(partialResponse bool, defaultMetadataTimeRange time.Duration) *labelsCodec {
	return &labelsCodec{
		partialResponse:          partialResponse,
		defaultMetadataTimeRange: defaultMetadataTimeRange,
	}
}

// MergeResponse 将同一类型的多个响应合并为一个, 其核心功能就是去重 + 结果升序排序.
func (c labelsCodec) MergeResponse(_ queryrange.Request, responses ...queryrange.Response) (queryrange.Response, error) {
	if len(responses) == 0 {
		return &ThanosLabelsResponse{
			Status: queryrange.StatusSuccess,
			Data:   []string{},
		}, nil
	}

	switch responses[0].(type) {
	case *ThanosLabelsResponse:
		if len(responses) == 1 {
			return responses[0], nil
		}
		// 集合, 用于去重
		set := make(map[string]struct{})

		for _, res := range responses {
			for _, value := range res.(*ThanosLabelsResponse).Data {
				if _, ok := set[value]; !ok {
					set[value] = struct{}{}
				}
			}
		}

		// 将集合转换为列表
		lbls := make([]string, 0, len(set))
		for label := range set {
			lbls = append(lbls, label)
		}

		sort.Strings(lbls)
		return &ThanosLabelsResponse{
			Status: queryrange.StatusSuccess,
			Data:   lbls,
		}, nil
	case *ThanosSeriesResponse:
		seriesData := make(labelpb.ZLabelSets, 0)

		// 去重
		uniqueSeries := make(map[string]struct{})
		for _, res := range responses {
			for _, series := range res.(*ThanosSeriesResponse).Data {
				s := series.PromLabels().String()
				if _, ok := uniqueSeries[s]; !ok {
					seriesData = append(seriesData, series)
					uniqueSeries[s] = struct{}{}
				}
			}
		}

		sort.Sort(seriesData)
		return &ThanosSeriesResponse{
			Status: queryrange.StatusSuccess,
			Data:   seriesData,
		}, nil
	default:
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "invalid response format")
	}
}

// DecodeRequest 将 http.Request(r) 解码为 ThanosLabelsRequest 或 ThanosSeriesRequest.
func (c labelsCodec) DecodeRequest(_ context.Context, r *http.Request, forwardHeaders []string) (queryrange.Request, error) {
	// func (r *Request) ParseForm() error
	// 把请求中的表单参数(query + body)解析成键值对, 填充到 r.From 和 r.PostForm 中.
	// query 参数写到 r.Form 中. POST/PUT/PATCH且content-type为x-www-form-urlencoded 时, 解析到 r.PostForm 和 r.Form 中.
	// Go 默认不会自动解析请求参数, 除非调用 ParseForm 或使用框架(Gin等)自动帮你调用.
	if err := r.ParseForm(); err != nil {
		return nil, httpgrpc.ErrorFromHTTPResponse(&httpgrpc.HTTPResponse{
			Code: int32(http.StatusBadRequest),
			Body: []byte(err.Error()),
		})
	}

	var (
		req queryrange.Request
		err error
	)
	switch op := getOperation(r); op {
	case labelNamesOp, labelValuesOp:
		req, err = c.parseLabelsRequest(r, op, forwardHeaders)
	case seriesOp:
		req, err = c.parseSeriesRequest(r, forwardHeaders)
	}
	if err != nil {
		return nil, err
	}

	return req, nil
}

// EncodeRequest 将 Request(ThanosLabelsRequest/ThanosSeriesRequest) 编码为 http.Request.
func (c labelsCodec) EncodeRequest(ctx context.Context, r queryrange.Request) (*http.Request, error) {
	var req *http.Request
	var err error
	switch thanosReq := r.(type) {
	case *ThanosLabelsRequest:
		var params = url.Values{
			"start":                      []string{encodeTime(thanosReq.Start)},
			"end":                        []string{encodeTime(thanosReq.End)},
			queryv1.PartialResponseParam: []string{strconv.FormatBool(thanosReq.PartialResponse)},
		}
		if len(thanosReq.Matchers) > 0 {
			params[queryv1.MatcherParam] = matchersToStringSlice(thanosReq.Matchers)
		}
		if len(thanosReq.StoreMatchers) > 0 {
			params[queryv1.StoreMatcherParam] = matchersToStringSlice(thanosReq.StoreMatchers)
		}

		if strings.Contains(thanosReq.Path, "/api/v1/label/") {
			u := &url.URL{
				Path:     thanosReq.Path,
				RawQuery: params.Encode(),
			}

			// 为什么不使用 http.NewRequest?
			// http.NewRequest 会做很多检查和初始化. 而这些对于当前操作来说是无用的且碍事的.

			req = &http.Request{
				Method:     http.MethodGet,
				RequestURI: u.String(), // This is what the httpgrpc code looks at.
				URL:        u,
				Body:       http.NoBody,
				Header:     http.Header{},
			}
		} else {
			req, err = http.NewRequest(http.MethodPost, thanosReq.Path, bytes.NewBufferString(params.Encode()))
			if err != nil {
				return nil, httpgrpc.Errorf(http.StatusBadRequest, "error creating request: %s", err.Error())
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		for _, hv := range thanosReq.Headers {
			for _, v := range hv.Values {
				req.Header.Add(hv.Name, v)
			}
		}

	case *ThanosSeriesRequest:
		var params = url.Values{
			"start":                      []string{encodeTime(thanosReq.Start)},
			"end":                        []string{encodeTime(thanosReq.End)},
			queryv1.DedupParam:           []string{strconv.FormatBool(thanosReq.Dedup)},
			queryv1.PartialResponseParam: []string{strconv.FormatBool(thanosReq.PartialResponse)},
			queryv1.ReplicaLabelsParam:   thanosReq.ReplicaLabels,
		}
		if len(thanosReq.Matchers) > 0 {
			params[queryv1.MatcherParam] = matchersToStringSlice(thanosReq.Matchers)
		}
		if len(thanosReq.StoreMatchers) > 0 {
			params[queryv1.StoreMatcherParam] = matchersToStringSlice(thanosReq.StoreMatchers)
		}

		req, err = http.NewRequest(http.MethodPost, thanosReq.Path, bytes.NewBufferString(params.Encode()))
		if err != nil {
			return nil, httpgrpc.Errorf(http.StatusBadRequest, "error creating request: %s", err.Error())
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, hv := range thanosReq.Headers {
			for _, v := range hv.Values {
				req.Header.Add(hv.Name, v)
			}
		}

	default:
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "invalid request format")
	}

	return req.WithContext(ctx), nil
}

// DecodeResponse 根据请求(req)类型将 http.Response 解码为 ThanosLabelsResponse/ThanosSeriesResponse.
func (c labelsCodec) DecodeResponse(ctx context.Context, r *http.Response, req queryrange.Request) (queryrange.Response, error) {
	if r.StatusCode/100 != 2 {
		// 这里不处理 io.ReadAll 错误信息, 是由于 r.StatusCode/100 != 2 本身就是在处理请求响应的错误信息.
		// 无论这里读取 body 是否产生错误都不影响此次请求本身就是错误的结果. 所以, 读取到就看错误信息, 读取不到就不看.
		body, _ := io.ReadAll(r.Body)
		return nil, httpgrpc.ErrorFromHTTPResponse(&httpgrpc.HTTPResponse{
			Code: int32(r.StatusCode),
			Body: body,
		})
	}
	log, _ := spanlogger.New(ctx, "ParseQueryResponse") //nolint:ineffassign,staticcheck
	defer log.Finish()

	// 这里是需要处理 io.ReadAll 错误的, 因为到这里请求返回是成功的.
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err) //nolint:errcheck
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error decoding response: %v", err)
	}

	log.LogFields(otlog.Int("bytes", len(buf)))

	switch req.(type) {
	case *ThanosLabelsRequest:
		var resp ThanosLabelsResponse
		if err := json.Unmarshal(buf, &resp); err != nil {
			return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error decoding response: %v", err)
		}
		for h, hv := range r.Header {
			resp.Headers = append(resp.Headers, &ResponseHeader{Name: h, Values: hv})
		}
		return &resp, nil
	case *ThanosSeriesRequest:
		var resp ThanosSeriesResponse
		if err := json.Unmarshal(buf, &resp); err != nil {
			return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error decoding response: %v", err)
		}
		for h, hv := range r.Header {
			resp.Headers = append(resp.Headers, &ResponseHeader{Name: h, Values: hv})
		}
		return &resp, nil
	default:
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "invalid request type")
	}
}

// EncodeResponse 将 ThanosLabelsResponse/ThanosSeriesResponse 编码为 http.Response.
func (c labelsCodec) EncodeResponse(ctx context.Context, res queryrange.Response) (*http.Response, error) {
	sp, _ := opentracing.StartSpanFromContext(ctx, "APIResponse.ToHTTPResponse")
	defer sp.Finish()

	var (
		b   []byte
		err error
	)
	switch resp := res.(type) {
	case *ThanosLabelsResponse:
		sp.LogFields(otlog.Int("labels", len(resp.Data)))
		b, err = json.Marshal(resp)
		if err != nil {
			return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error encoding response: %v", err)
		}
	case *ThanosSeriesResponse:
		sp.LogFields(otlog.Int("series", len(resp.Data)))
		b, err = json.Marshal(resp)
		if err != nil {
			return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error encoding response: %v", err)
		}
	default:
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "invalid response format")
	}

	sp.LogFields(otlog.Int("bytes", len(b)))
	resp := http.Response{
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:       io.NopCloser(bytes.NewBuffer(b)),
		StatusCode: http.StatusOK,
	}
	return &resp, nil
}

// parseLabelsRequest 从 http.Request(r) 中提取参数构建 ThanosLabelsRequest.
func (c labelsCodec) parseLabelsRequest(r *http.Request, op string, forwardHeaders []string) (queryrange.Request, error) {
	var (
		result ThanosLabelsRequest
		err    error
	)
	result.Start, result.End, err = parseMetadataTimeRange(r, c.defaultMetadataTimeRange)
	if err != nil {
		return nil, err
	}

	result.Matchers, err = parseMatchersParam(r.Form, queryv1.MatcherParam)
	if err != nil {
		return nil, err
	}

	result.PartialResponse, err = parsePartialResponseParam(r.FormValue(queryv1.PartialResponseParam), c.partialResponse)
	if err != nil {
		return nil, err
	}

	result.StoreMatchers, err = parseMatchersParam(r.Form, queryv1.StoreMatcherParam)
	if err != nil {
		return nil, err
	}

	result.Path = r.URL.Path

	if op == labelValuesOp {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) > 1 {
			result.Label = parts[len(parts)-2]
		}
	}

	for _, value := range r.Header.Values(cacheControlHeader) {
		if strings.Contains(value, noStoreValue) {
			result.CachingOptions.Disabled = true
			break
		}
	}

	// Include the specified headers from http request in prometheusRequest.
	for _, header := range forwardHeaders {
		for h, hv := range r.Header {
			if strings.EqualFold(h, header) {
				result.Headers = append(result.Headers, &RequestHeader{Name: h, Values: hv})
				break
			}
		}
	}

	return &result, nil
}

// parseSeriesRequest 从 http.Request(r) 中提取参数构建 ThanosSeriesRequest.
func (c labelsCodec) parseSeriesRequest(r *http.Request, forwardHeaders []string) (queryrange.Request, error) {
	var (
		result ThanosSeriesRequest
		err    error
	)

	result.Start, result.End, err = parseMetadataTimeRange(r, c.defaultMetadataTimeRange)
	if err != nil {
		return nil, err
	}

	result.Matchers, err = parseMatchersParam(r.Form, queryv1.MatcherParam)
	if err != nil {
		return nil, err
	}

	result.Dedup, err = parseEnableDedupParam(r.FormValue(queryv1.DedupParam))
	if err != nil {
		return nil, err
	}

	result.PartialResponse, err = parsePartialResponseParam(r.FormValue(queryv1.PartialResponseParam), c.partialResponse)
	if err != nil {
		return nil, err
	}

	if len(r.Form[queryv1.ReplicaLabelsParam]) > 0 {
		result.ReplicaLabels = r.Form[queryv1.ReplicaLabelsParam]
	}

	result.StoreMatchers, err = parseMatchersParam(r.Form, queryv1.StoreMatcherParam)
	if err != nil {
		return nil, err
	}

	result.Path = r.URL.Path

	for _, value := range r.Header.Values(cacheControlHeader) {
		if strings.Contains(value, noStoreValue) {
			result.CachingOptions.Disabled = true
			break
		}
	}

	for _, header := range forwardHeaders {
		for h, hv := range r.Header {
			if strings.EqualFold(h, header) {
				result.Headers = append(result.Headers, &RequestHeader{Name: h, Values: hv})
				break
			}
		}
	}

	return &result, nil
}

// parseMetadataTimeRange 从请求(r)中提取参数 start, end 并按照毫秒时间戳返回. 若 start 参数不存在, 则返回 now - defaultMetadataTimeRange.
// 若 start 参数不存在, 则返回 now. 该函数确保 end >= start.
func parseMetadataTimeRange(r *http.Request, defaultMetadataTimeRange time.Duration) (int64, int64, error) {
	// If start and end time not specified as query parameter, we get the range from the beginning of time by default.
	var defaultStartTime, defaultEndTime time.Time
	if defaultMetadataTimeRange == 0 {
		defaultStartTime = infMinTime
		defaultEndTime = infMaxTime
	} else {
		now := time.Now()
		defaultStartTime = now.Add(-defaultMetadataTimeRange)
		defaultEndTime = now
	}

	start, err := parseTimeParam(r, "start", defaultStartTime)
	if err != nil {
		return 0, 0, err
	}
	end, err := parseTimeParam(r, "end", defaultEndTime)
	if err != nil {
		return 0, 0, err
	}
	if end < start {
		return 0, 0, errEndBeforeStart
	}

	return start, end, nil
}

// parseTimeParam 从请求中提取时间(paramName)参数值, 并以毫秒数值返回. 若无该参数则返回默认时间(defaultValue)毫秒值.
func parseTimeParam(r *http.Request, paramName string, defaultValue time.Time) (int64, error) {
	val := r.FormValue(paramName)
	if val == "" {
		return timestamp.FromTime(defaultValue), nil
	}
	result, err := cortexutil.ParseTime(val)
	if err != nil {
		return 0, err
	}
	return result, nil
}
