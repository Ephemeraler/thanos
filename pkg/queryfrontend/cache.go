// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package queryfrontend

import (
	"fmt"

	"github.com/thanos-io/thanos/internal/cortex/querier/queryrange"
	"github.com/thanos-io/thanos/pkg/compact/downsample"
)

// thanosCacheKeyGenerator 是一个用于在确定缓存键时使用分割时间区间(split interval)的工具.
type thanosCacheKeyGenerator struct {
	resolutions []int64
}

func newThanosCacheKeyGenerator() thanosCacheKeyGenerator {
	return thanosCacheKeyGenerator{
		resolutions: []int64{downsample.ResLevel2, downsample.ResLevel1, downsample.ResLevel0},
	}
}

// GenerateCacheKey 根据请求(Request)和时间区间(interval)生成一个缓存键.
// TODO(yeya24): 添加其他请求参数作为缓存键的一部分.
func (t thanosCacheKeyGenerator) GenerateCacheKey(userID string, r queryrange.Request) string {
	if sr, ok := r.(SplitRequest); ok {
		splitInterval := sr.GetSplitInterval().Milliseconds()
		currentInterval := r.GetStart() / splitInterval

		switch tr := r.(type) {
		case *ThanosQueryRangeRequest:
			i := 0
			for ; i < len(t.resolutions) && t.resolutions[i] > tr.MaxSourceResolution; i++ {
			}
			shardInfoKey := generateShardInfoKey(tr)
			return fmt.Sprintf("fe:%s:%s:%d:%d:%d:%d:%s:%d:%s", userID, tr.Query, tr.Step, splitInterval, currentInterval, i, shardInfoKey, tr.LookbackDelta, tr.Engine)
		case *ThanosLabelsRequest:
			return fmt.Sprintf("fe:%s:%s:%s:%d:%d", userID, tr.Label, tr.Matchers, splitInterval, currentInterval)
		case *ThanosSeriesRequest:
			return fmt.Sprintf("fe:%s:%s:%d:%d", userID, tr.Matchers, splitInterval, currentInterval)
		}
	}

	// all possible request types are already covered
	panic("request type not supported")
}

func generateShardInfoKey(r *ThanosQueryRangeRequest) string {
	if r.ShardInfo == nil {
		return "-"
	}
	return fmt.Sprintf("%d:%d", r.ShardInfo.TotalShards, r.ShardInfo.ShardIndex)
}
