// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package queryrange

import (
	"context"
)

// StepAlignMiddleware 步长对齐中间层.
var StepAlignMiddleware = MiddlewareFunc(func(next Handler) Handler {
	return stepAlign{
		next: next,
	}
})

// stepAlign 是中间层 stepAlign 的具体实现.
type stepAlign struct {
	next Handler
}

// Do 该方法主要是修改请求中的 start 和 end 的值, 使其向下对齐到 step 的整数倍.
func (s stepAlign) Do(ctx context.Context, r Request) (Response, error) {
	// 这块的计算将 start 与 end 向下取整到 "step" 的整数倍.
	start := (r.GetStart() / r.GetStep()) * r.GetStep()
	end := (r.GetEnd() / r.GetStep()) * r.GetStep()
	// 将对齐的 start 和 end 修改到请求中.
	return s.next.Do(ctx, r.WithStartEnd(start, end))
}
