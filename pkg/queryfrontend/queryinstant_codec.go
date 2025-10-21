// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package queryfrontend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/weaveworks/common/httpgrpc"

	"github.com/thanos-io/thanos/internal/cortex/cortexpb"
	"github.com/thanos-io/thanos/internal/cortex/querier/queryrange"
	cortexutil "github.com/thanos-io/thanos/internal/cortex/util"
	"github.com/thanos-io/thanos/internal/cortex/util/spanlogger"
	queryv1 "github.com/thanos-io/thanos/pkg/api/query"
	"github.com/thanos-io/thanos/pkg/extpromql"
)

// queryInstantCodec Instant Query 请求响应编解码器, 用于  与 http.Request/http.Response 之间进行编解码.
// Instant Query 请求包括 /api/v1/query.
type queryInstantCodec struct {
	partialResponse bool // 对应 query-range.partial-response  命令行参数, 默认值: true
}

// NewThanosQueryInstantCodec 创建 queryInstantCodec 实例.
func NewThanosQueryInstantCodec(partialResponse bool) *queryInstantCodec {
	return &queryInstantCodec{
		partialResponse: partialResponse,
	}
}

// MergeResponse merges multiple responses into a single response. For instant query
// only vector and matrix responses will be merged because other types of queries
// are not shardable like number literal, string literal, scalar, etc.
// 当前只会合并 matrix, vector 类型的响应, 其他类型的响应不会合并.
func (c queryInstantCodec) MergeResponse(req queryrange.Request, responses ...queryrange.Response) (queryrange.Response, error) {
	// responses 为空和唯一都不需要合并, 直接返回即可.
	if len(responses) == 0 {
		return queryrange.NewEmptyPrometheusInstantQueryResponse(), nil
	} else if len(responses) == 1 {
		return responses[0], nil
	}

	// 先将 response 转换成 PrometheusInstantQueryResponse.
	promResponses := make([]*queryrange.PrometheusInstantQueryResponse, 0, len(responses))
	for _, resp := range responses {
		promResponses = append(promResponses, resp.(*queryrange.PrometheusInstantQueryResponse))
	}

	var analyzes []*queryrange.Analysis
	for i := range promResponses {
		if promResponses[i].Data.GetAnalysis() == nil {
			continue
		}

		analyzes = append(analyzes, promResponses[i].Data.GetAnalysis())
	}

	var res queryrange.Response
	// [?]: 为什么只判断第一个 response 的类型?
	// [可能性推测] 首先, 合并结果是合并来自不同源, 相同查询语句的结果. 所以尽管有多个响应, 但是响应类型应该是唯一的.
	switch promResponses[0].Data.ResultType {
	case model.ValMatrix.String():
		res = &queryrange.PrometheusInstantQueryResponse{
			Status: queryrange.StatusSuccess,
			Data: queryrange.PrometheusInstantQueryData{
				ResultType: model.ValMatrix.String(),
				Result: queryrange.PrometheusInstantQueryResult{
					Result: &queryrange.PrometheusInstantQueryResult_Matrix{
						Matrix: matrixMerge(promResponses),
					},
				},
				Analysis: queryrange.AnalyzesMerge(analyzes...),
				Stats:    queryrange.StatsMerge(responses),
			},
		}
	default:
		v, err := vectorMerge(req, promResponses)
		if err != nil {
			return nil, err
		}
		res = &queryrange.PrometheusInstantQueryResponse{
			Status: queryrange.StatusSuccess,
			Data: queryrange.PrometheusInstantQueryData{
				ResultType: model.ValVector.String(),
				Result: queryrange.PrometheusInstantQueryResult{
					Result: &queryrange.PrometheusInstantQueryResult_Vector{
						Vector: v,
					},
				},
				Analysis: queryrange.AnalyzesMerge(analyzes...),
				Stats:    queryrange.StatsMerge(responses),
			},
		}
	}

	return res, nil
}

// DecodeRequest 将 http.Request(r) 解码为 ThanosQueryInstantRequest.
func (c queryInstantCodec) DecodeRequest(_ context.Context, r *http.Request, forwardHeaders []string) (queryrange.Request, error) {
	var (
		// Thanos Query Instant API 支持的参数
		result ThanosQueryInstantRequest
		err    error
	)

	if len(r.FormValue("time")) > 0 {
		result.Time, err = cortexutil.ParseTime(r.FormValue("time"))
		if err != nil {
			return nil, err
		}
	}

	if len(r.FormValue("analyze")) > 0 {
		analyze, err := strconv.ParseBool(r.FormValue("analyze"))
		if err != nil {
			return nil, err
		}
		result.Analyze = analyze
	}

	result.Dedup, err = parseEnableDedupParam(r.FormValue(queryv1.DedupParam))
	if err != nil {
		return nil, err
	}

	if r.FormValue(queryv1.MaxSourceResolutionParam) == "auto" {
		result.AutoDownsampling = true
	} else {
		result.MaxSourceResolution, err = parseDownsamplingParamMillis(r.FormValue(queryv1.MaxSourceResolutionParam))
		if err != nil {
			return nil, err
		}
	}

	result.PartialResponse, err = parsePartialResponseParam(r.FormValue(queryv1.PartialResponseParam), c.partialResponse)
	if err != nil {
		return nil, err
	}

	// 这个代码写的我还挺疑惑的, 为什么要判断是不是为空? 本来 result.ReplicaLabels 就是[]string, 直接赋值不行吗?
	// 不行, 如果参数没传, r.Form[queryv1.ReplicaLabelsParam] 就是 nil
	if len(r.Form[queryv1.ReplicaLabelsParam]) > 0 {
		result.ReplicaLabels = r.Form[queryv1.ReplicaLabelsParam]
	}

	result.StoreMatchers, err = parseMatchersParam(r.Form, queryv1.StoreMatcherParam)
	if err != nil {
		return nil, err
	}

	result.ShardInfo, err = parseShardInfo(r.Form, queryv1.ShardInfoParam)
	if err != nil {
		return nil, err
	}

	result.LookbackDelta, err = parseLookbackDelta(r.Form, queryv1.LookbackDeltaParam)
	if err != nil {
		return nil, err
	}

	result.Query = r.FormValue("query")
	result.Path = r.URL.Path
	result.Engine = r.FormValue("engine")
	result.Stats = r.FormValue(queryv1.Stats)

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

// EncodeRequest 将 ThanosQueryInstantRequest 转换成 http.Request, 并使用 ctx 作为 http.Request.Context.
func (c queryInstantCodec) EncodeRequest(ctx context.Context, r queryrange.Request) (*http.Request, error) {
	thanosReq, ok := r.(*ThanosQueryInstantRequest)
	if !ok {
		return nil, httpgrpc.Errorf(http.StatusBadRequest, "invalid request format")
	}
	params := url.Values{
		"query":                      []string{thanosReq.Query},
		queryv1.DedupParam:           []string{strconv.FormatBool(thanosReq.Dedup)},
		queryv1.QueryAnalyzeParam:    []string{strconv.FormatBool(thanosReq.Analyze)},
		queryv1.PartialResponseParam: []string{strconv.FormatBool(thanosReq.PartialResponse)},
		queryv1.EngineParam:          []string{thanosReq.Engine},
		queryv1.ReplicaLabelsParam:   thanosReq.ReplicaLabels,
		queryv1.Stats:                []string{thanosReq.Stats},
	}

	if thanosReq.Time > 0 {
		params["time"] = []string{encodeTime(thanosReq.Time)}
	}
	if thanosReq.AutoDownsampling {
		params[queryv1.MaxSourceResolutionParam] = []string{"auto"}
	} else if thanosReq.MaxSourceResolution != 0 {
		// Add this param only if it is set. Set to 0 will impact
		// auto-downsampling in the querier.
		params[queryv1.MaxSourceResolutionParam] = []string{encodeDurationMillis(thanosReq.MaxSourceResolution)}
	}

	if len(thanosReq.StoreMatchers) > 0 {
		params[queryv1.StoreMatcherParam] = matchersToStringSlice(thanosReq.StoreMatchers)
	}

	if thanosReq.ShardInfo != nil {
		data, err := encodeShardInfo(thanosReq.ShardInfo)
		if err != nil {
			return nil, err
		}
		params[queryv1.ShardInfoParam] = []string{data}
	}

	if thanosReq.LookbackDelta > 0 {
		params[queryv1.LookbackDeltaParam] = []string{encodeDurationMillis(thanosReq.LookbackDelta)}
	}

	req, err := http.NewRequest(http.MethodPost, thanosReq.Path, bytes.NewBufferString(params.Encode()))
	if err != nil {
		return nil, httpgrpc.Errorf(http.StatusBadRequest, "error creating request: %s", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, hv := range thanosReq.Headers {
		for _, v := range hv.Values {
			req.Header.Add(hv.Name, v)
		}
	}
	return req.WithContext(ctx), nil
}

// EncodeResponse 将 PrometheusInstantQueryResponse 转换成 http.Response, 同时将响应体头也写入 http.Response.
func (c queryInstantCodec) EncodeResponse(ctx context.Context, res queryrange.Response) (*http.Response, error) {
	sp, _ := opentracing.StartSpanFromContext(ctx, "APIResponse.ToHTTPResponse")
	defer sp.Finish()

	a, ok := res.(*queryrange.PrometheusInstantQueryResponse)
	if !ok {
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "invalid response format")
	}

	b, err := json.Marshal(a)
	if err != nil {
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error encoding response: %v", err)
	}
	sp.LogFields(otlog.Int("bytes", len(b)))

	resp := http.Response{
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(bytes.NewBuffer(b)),
		StatusCode:    http.StatusOK,
		ContentLength: int64(len(b)),
	}
	return &resp, nil
}

// DecodeResponse 将 http.Response 转换成 PrometheusInstantQueryResponse, 同时将响应体头也写入 PrometheusInstantQueryResponse.
func (c queryInstantCodec) DecodeResponse(ctx context.Context, r *http.Response, req queryrange.Request) (queryrange.Response, error) {
	// 响应码校验.
	if r.StatusCode/100 != 2 {
		body, _ := io.ReadAll(r.Body)
		return nil, httpgrpc.ErrorFromHTTPResponse(&httpgrpc.HTTPResponse{
			Code: int32(r.StatusCode),
			Body: body,
		})
	}
	log, ctx := spanlogger.New(ctx, "ParseQueryInstantResponse") //nolint:ineffassign,staticcheck
	defer log.Finish()

	// 读取响应体内容
	buf, err := queryrange.BodyBuffer(r)
	if err != nil {
		log.Error(err) //nolint:errcheck
		return nil, err
	}
	log.LogFields(otlog.Int("bytes", len(buf)))

	// 将响应体解析到 queryrange.PrometheusInstantQueryResponse
	var resp queryrange.PrometheusInstantQueryResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		return nil, httpgrpc.Errorf(http.StatusInternalServerError, "error decoding response: %v", err)
	}

	// 将响应体头写入到 queryrange.PrometheusInstantQueryResponse.Headers
	for h, hv := range r.Header {
		resp.Headers = append(resp.Headers, &queryrange.PrometheusResponseHeader{Name: h, Values: hv})
	}
	return &resp, nil
}

// vectorMerge
func vectorMerge(req queryrange.Request, resps []*queryrange.PrometheusInstantQueryResponse) (*queryrange.Vector, error) {
	output := map[string]*queryrange.Sample{}
	metrics := []string{} // Used to preserve the order for topk and bottomk.
	// [?] 不知道在干什么.
	sortPlan, err := sortPlanForQuery(req.GetQuery())
	if err != nil {
		return nil, err
	}
	for _, resp := range resps {
		if resp == nil {
			continue
		}
		// Merge vector result samples only. Skip other types such as
		// string, scalar as those are not sharable.
		if resp.Data.Result.GetVector() == nil {
			continue
		}
		for _, sample := range resp.Data.Result.GetVector().Samples {
			s := sample
			if s == nil {
				continue
			}
			metric := cortexpb.FromLabelAdaptersToLabels(sample.Labels).String()
			if existingSample, ok := output[metric]; !ok {
				output[metric] = s
				metrics = append(metrics, metric) // Preserve the order of metric.
			} else if existingSample.Timestamp < s.Timestamp {
				// Choose the latest sample if we see overlap.
				output[metric] = s
			}
		}
	}

	result := &queryrange.Vector{
		Samples: make([]*queryrange.Sample, 0, len(output)),
	}

	if len(output) == 0 {
		return result, nil
	}

	if sortPlan == mergeOnly {
		for _, k := range metrics {
			result.Samples = append(result.Samples, output[k])
		}
		return result, nil
	}

	type pair struct {
		metric string
		s      *queryrange.Sample
	}

	samples := make([]*pair, 0, len(output))
	for k, v := range output {
		samples = append(samples, &pair{
			metric: k,
			s:      v,
		})
	}

	sort.Slice(samples, func(i, j int) bool {
		// Order is determined by vector
		switch sortPlan {
		case sortByValuesAsc:
			return samples[i].s.SampleValue < samples[j].s.SampleValue
		case sortByValuesDesc:
			return samples[i].s.SampleValue > samples[j].s.SampleValue
		}
		return samples[i].metric < samples[j].metric
	})

	for _, p := range samples {
		result.Samples = append(result.Samples, p.s)
	}
	return result, nil
}

type sortPlan int

const (
	mergeOnly        sortPlan = 0
	sortByValuesAsc  sortPlan = 1
	sortByValuesDesc sortPlan = 2
	sortByLabels     sortPlan = 3
)

// sortPlanForQuery
func sortPlanForQuery(q string) (sortPlan, error) {
	expr, err := extpromql.ParseExpr(q)
	if err != nil {
		return 0, err
	}
	// Check if the root expression is topk or bottomk
	if aggr, ok := expr.(*parser.AggregateExpr); ok {
		if aggr.Op == parser.TOPK || aggr.Op == parser.BOTTOMK {
			return mergeOnly, nil
		}
	}
	checkForSort := func(expr parser.Expr) (sortAsc, sortDesc bool) {
		if n, ok := expr.(*parser.Call); ok {
			if n.Func != nil {
				if n.Func.Name == "sort" {
					sortAsc = true
				}
				if n.Func.Name == "sort_desc" {
					sortDesc = true
				}
			}
		}
		return sortAsc, sortDesc
	}
	// Check the root expression for sort
	if sortAsc, sortDesc := checkForSort(expr); sortAsc || sortDesc {
		if sortAsc {
			return sortByValuesAsc, nil
		}
		return sortByValuesDesc, nil
	}

	// If the root expression is a binary expression, check the LHS and RHS for sort
	if bin, ok := expr.(*parser.BinaryExpr); ok {
		if sortAsc, sortDesc := checkForSort(bin.LHS); sortAsc || sortDesc {
			if sortAsc {
				return sortByValuesAsc, nil
			}
			return sortByValuesDesc, nil
		}
		if sortAsc, sortDesc := checkForSort(bin.RHS); sortAsc || sortDesc {
			if sortAsc {
				return sortByValuesAsc, nil
			}
			return sortByValuesDesc, nil
		}
	}
	return sortByLabels, nil
}

// matrixMerge 合并 PrometheusInstantQueryResponse 中的 matrix 结果.
func matrixMerge(resps []*queryrange.PrometheusInstantQueryResponse) *queryrange.Matrix {
	output := map[string]*queryrange.SampleStream{}
	for _, resp := range resps {
		if resp == nil {
			continue
		}
		// Merge matrix result samples only. Skip other types such as
		// string, scalar as those are not sharable.
		if resp.Data.Result.GetMatrix() == nil {
			continue
		}
		for _, stream := range resp.Data.Result.GetMatrix().SampleStreams {
			// [?] 这个应该不只是metric, 还应该包含 labels? 应该是作为唯一标识使用.
			metric := cortexpb.FromLabelAdaptersToLabels(stream.Labels).String()
			existing, ok := output[metric]
			if !ok {
				existing = &queryrange.SampleStream{
					Labels: stream.Labels,
				}
			}
			// We need to make sure we don't repeat samples. This causes some visualizations to be broken in Grafana.
			// The prometheus API is inclusive of start and end timestamps.
			// stream 合并到 existing 中.
			if len(existing.Samples) > 0 && len(stream.Samples) > 0 {
				// 获取 existing 中最晚时间.
				existingEndTs := existing.Samples[len(existing.Samples)-1].TimestampMs
				if existingEndTs == stream.Samples[0].TimestampMs {
					// existing 最晚时间 == stream 最早时间, 只有一个点重叠.
					// [v] 这块不会 panic 吗? 当 stream.Samples 只有 1 个元素的时候?
					// 不会, 因为 len() > 0 确保切片中至少有1个元素. 而 [1:] 是合法的.
					stream.Samples = stream.Samples[1:]
				} else if existingEndTs > stream.Samples[0].TimestampMs {
					// 如果 existing 最晚时间 > stream 最早时间, 说明有重叠. 重叠范围不好确定.
					stream.Samples = queryrange.SliceSamples(stream.Samples, existingEndTs)
				} // else there is no overlap, yay!
			}
			// 将非重叠数据合并到 existing 中.
			// [?] 那这里会不会存在 下一个 stream 在 existing 中间的范围, 而这个中间正好为空间隔
			existing.Samples = append(existing.Samples, stream.Samples...)
			output[metric] = existing
		}
	}

	keys := make([]string, 0, len(output))
	for key := range output {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := &queryrange.Matrix{
		SampleStreams: make([]*queryrange.SampleStream, 0, len(output)),
	}
	for _, key := range keys {
		result.SampleStreams = append(result.SampleStreams, output[key])
	}

	return result
}
