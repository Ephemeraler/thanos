// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package queryrange

import (
	"context"
)

// StepAlignMiddleware  将查询的起始时间（start）与结束时间（end）调整到采样步长（step）的整数倍边界上，使得整个时间区间能够被步长整除..
var StepAlignMiddleware = MiddlewareFunc(func(next Handler) Handler {
	return stepAlign{
		next: next,
	}
})

type stepAlign struct {
	next Handler
}

// Do 主要做步长对齐. 将查询的起始时间（start）与结束时间（end）调整到采样步长（step）的整数倍边界上，使得整个时间区间能够被步长整除.
func (s stepAlign) Do(ctx context.Context, r Request) (Response, error) {
	// start <= r.GetStart
	start := (r.GetStart() / r.GetStep()) * r.GetStep()
	// end <= r.GetEnd
	end := (r.GetEnd() / r.GetStep()) * r.GetStep()
	return s.next.Do(ctx, r.WithStartEnd(start, end))
}

// a = b * c + d
