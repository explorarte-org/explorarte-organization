package decisiongraph

import (
	"fmt"
	"math"
	"time"
)

type BudgetLimits struct {
	MaxNodes         int64
	MaxDepth         int64
	MaxParallelNodes int64
	MaxModelCalls    int64
	MaxInputTokens   int64
	MaxOutputTokens  int64
	MaxReplans       int64
	MaxVerifications int64
	MaxWallTime      time.Duration
}

type BudgetUsage struct {
	Nodes         int64
	Depth         int64
	ParallelNodes int64
	ModelCalls    int64
	InputTokens   int64
	OutputTokens  int64
	Replans       int64
	Verifications int64
	WallTime      time.Duration
}

type BudgetDimensionError struct {
	Dimension string
	Limit     int64
	Used      int64
	Requested int64
}

func (e *BudgetDimensionError) Error() string {
	return fmt.Sprintf("%v: %s limit=%d used=%d requested=%d", ErrBudgetExceeded, e.Dimension, e.Limit, e.Used, e.Requested)
}

func (e *BudgetDimensionError) Unwrap() error { return ErrBudgetExceeded }

func (l BudgetLimits) Validate() error {
	values := []int64{l.MaxNodes, l.MaxDepth, l.MaxParallelNodes, l.MaxModelCalls, l.MaxInputTokens, l.MaxOutputTokens, l.MaxReplans, l.MaxVerifications}
	for _, value := range values {
		if value <= 0 {
			return ErrInvalidBudget
		}
	}
	if l.MaxWallTime <= 0 {
		return ErrInvalidBudget
	}
	return nil
}

func (u BudgetUsage) Validate() error {
	values := []int64{u.Nodes, u.Depth, u.ParallelNodes, u.ModelCalls, u.InputTokens, u.OutputTokens, u.Replans, u.Verifications}
	for _, value := range values {
		if value < 0 {
			return ErrInvalidBudget
		}
	}
	if u.WallTime < 0 {
		return ErrInvalidBudget
	}
	return nil
}

func (l BudgetLimits) Reserve(current, delta BudgetUsage) (BudgetUsage, error) {
	if err := l.Validate(); err != nil {
		return BudgetUsage{}, err
	}
	if err := current.Validate(); err != nil {
		return BudgetUsage{}, err
	}
	if err := delta.Validate(); err != nil {
		return BudgetUsage{}, err
	}

	next := BudgetUsage{}
	checks := []struct {
		name        string
		limit       int64
		current     int64
		delta       int64
		destination *int64
	}{
		{"nodes", l.MaxNodes, current.Nodes, delta.Nodes, &next.Nodes},
		{"depth", l.MaxDepth, current.Depth, delta.Depth, &next.Depth},
		{"parallel_nodes", l.MaxParallelNodes, current.ParallelNodes, delta.ParallelNodes, &next.ParallelNodes},
		{"model_calls", l.MaxModelCalls, current.ModelCalls, delta.ModelCalls, &next.ModelCalls},
		{"input_tokens", l.MaxInputTokens, current.InputTokens, delta.InputTokens, &next.InputTokens},
		{"output_tokens", l.MaxOutputTokens, current.OutputTokens, delta.OutputTokens, &next.OutputTokens},
		{"replans", l.MaxReplans, current.Replans, delta.Replans, &next.Replans},
		{"verifications", l.MaxVerifications, current.Verifications, delta.Verifications, &next.Verifications},
	}
	for _, check := range checks {
		value, err := safeAdd(check.current, check.delta)
		if err != nil {
			return BudgetUsage{}, fmt.Errorf("%w: %s", err, check.name)
		}
		if value > check.limit {
			return BudgetUsage{}, &BudgetDimensionError{Dimension: check.name, Limit: check.limit, Used: check.current, Requested: check.delta}
		}
		*check.destination = value
	}
	if delta.WallTime > time.Duration(math.MaxInt64-int64(current.WallTime)) {
		return BudgetUsage{}, fmt.Errorf("%w: wall_time", ErrBudgetOverflow)
	}
	next.WallTime = current.WallTime + delta.WallTime
	if next.WallTime > l.MaxWallTime {
		return BudgetUsage{}, &BudgetDimensionError{Dimension: "wall_time_nanoseconds", Limit: int64(l.MaxWallTime), Used: int64(current.WallTime), Requested: int64(delta.WallTime)}
	}
	return next, nil
}

func safeAdd(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, ErrBudgetOverflow
	}
	return left + right, nil
}
