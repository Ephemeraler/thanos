// Copyright (c) The Cortex Authors.
// Licensed under the Apache License 2.0.

package util

import (
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/common/model"
	"github.com/weaveworks/common/httpgrpc"
)

const (
	// 倍数关系, 表示1毫秒中有多少个纳秒
	nanosecondsInMillisecond = int64(time.Millisecond / time.Nanosecond)
)

// TimeToMillis 将时间以 Unix 时间戳(毫秒) 形式返回.
func TimeToMillis(t time.Time) int64 {
	return t.UnixMilli()
	// return t.UnixNano() / nanosecondsInMillisecond
}

// TimeFromMillis is a helper to turn milliseconds -> time.Time
func TimeFromMillis(ms int64) time.Time {
	return time.Unix(0, ms*nanosecondsInMillisecond)
}

// FormatTimeMillis returns a human readable version of the input time (in milliseconds).
func FormatTimeMillis(ms int64) string {
	return TimeFromMillis(ms).String()
}

// FormatTimeModel returns a human readable version of the input time.
func FormatTimeModel(t model.Time) string {
	return TimeFromMillis(int64(t)).String()
}

// ParseTime 解析时间字符串, 并返回毫秒数. 时间字符串支持 RFC3339Nano 和以秒为单位的 Unix 时间戳(可带小数用于表示子秒精度).
func ParseTime(s string) (int64, error) {
	if t, err := strconv.ParseFloat(s, 64); err == nil {
		s, ns := math.Modf(t)
		// 保留三位小数.
		ns = math.Round(ns*1000) / 1000
		tm := time.Unix(int64(s), int64(ns*float64(time.Second)))
		return TimeToMillis(tm), nil
	}

	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return TimeToMillis(t), nil
	}
	return 0, httpgrpc.Errorf(http.StatusBadRequest, "cannot parse %q to a valid timestamp", s)
}

// DurationWithJitter returns random duration from "input - input*variance" to "input + input*variance" interval.
func DurationWithJitter(input time.Duration, variancePerc float64) time.Duration {
	// No duration? No jitter.
	if input == 0 {
		return 0
	}

	variance := int64(float64(input) * variancePerc)
	jitter := rand.Int63n(variance*2) - variance

	return input + time.Duration(jitter)
}

// DurationWithPositiveJitter returns random duration from "input" to "input + input*variance" interval.
func DurationWithPositiveJitter(input time.Duration, variancePerc float64) time.Duration {
	// No duration? No jitter.
	if input == 0 {
		return 0
	}

	variance := int64(float64(input) * variancePerc)
	jitter := rand.Int63n(variance)

	return input + time.Duration(jitter)
}

// NewDisableableTicker essentially wraps NewTicker but allows the ticker to be disabled by passing
// zero duration as the interval. Returns a function for stopping the ticker, and the ticker channel.
func NewDisableableTicker(interval time.Duration) (func(), <-chan time.Time) {
	if interval == 0 {
		return func() {}, nil
	}

	tick := time.NewTicker(interval)
	return func() { tick.Stop() }, tick.C
}
