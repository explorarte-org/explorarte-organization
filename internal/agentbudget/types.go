package agentbudget

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// Limits are the seven simultaneous ceilings a budget enforces. All fields
// are required and must be positive — there is no "0 means unlimited"
// escape hatch, the same fail-closed posture as the rest of this ledger
// work.
type Limits struct {
	MaxUSD        modelpricing.USDNanos
	MaxTokens     int64
	MaxModelCalls int64
	MaxWallTimeMS int64
	MaxDepth      int64
	MaxRetries    int64
	MaxSubagents  int64
}

func (l Limits) Validate() error {
	if l.MaxUSD <= 0 || l.MaxTokens <= 0 || l.MaxModelCalls <= 0 || l.MaxWallTimeMS <= 0 || l.MaxDepth <= 0 || l.MaxRetries <= 0 || l.MaxSubagents <= 0 {
		return fmt.Errorf("%w: every budget dimension must be positive", ErrInvalidRequest)
	}
	return nil
}

// Usage is both a running total (current consumption of a budget) and, when
// passed to Reserve as a delta, the amount one operation wants to consume.
// Depth is the exception: it is not cumulative, it is the current depth in
// the tree, so a delta of 1 means "one level deeper," never "one more unit
// consumed."
type Usage struct {
	UsedUSD        modelpricing.USDNanos
	UsedTokens     int64
	UsedModelCalls int64
	UsedWallTimeMS int64
	Depth          int64
	UsedRetries    int64
	UsedSubagents  int64
}

// Reserve is a pure, overflow-free check-and-add: it returns the usage that
// would result from applying delta to current, or ErrBudgetExceeded if any
// single dimension would cross its limit. It never partially applies —
// either every dimension clears, or none of them do.
func Reserve(current, delta Usage, limits Limits) (Usage, error) {
	next := Usage{
		UsedUSD:        current.UsedUSD + delta.UsedUSD,
		UsedTokens:     current.UsedTokens + delta.UsedTokens,
		UsedModelCalls: current.UsedModelCalls + delta.UsedModelCalls,
		UsedWallTimeMS: current.UsedWallTimeMS + delta.UsedWallTimeMS,
		Depth:          current.Depth + delta.Depth,
		UsedRetries:    current.UsedRetries + delta.UsedRetries,
		UsedSubagents:  current.UsedSubagents + delta.UsedSubagents,
	}
	switch {
	case delta.UsedUSD < 0 || delta.UsedTokens < 0 || delta.UsedModelCalls < 0 || delta.UsedWallTimeMS < 0 || delta.Depth < 0 || delta.UsedRetries < 0 || delta.UsedSubagents < 0:
		return Usage{}, fmt.Errorf("%w: delta dimensions must be non-negative", ErrInvalidRequest)
	case next.UsedUSD < current.UsedUSD || next.UsedTokens < current.UsedTokens || next.UsedModelCalls < current.UsedModelCalls ||
		next.UsedWallTimeMS < current.UsedWallTimeMS || next.Depth < current.Depth || next.UsedRetries < current.UsedRetries || next.UsedSubagents < current.UsedSubagents:
		return Usage{}, fmt.Errorf("%w: budget usage overflowed", ErrInvalidRequest)
	case next.UsedUSD > limits.MaxUSD:
		return Usage{}, fmt.Errorf("%w: usd %s exceeds max %s", ErrBudgetExceeded, next.UsedUSD, limits.MaxUSD)
	case next.UsedTokens > limits.MaxTokens:
		return Usage{}, fmt.Errorf("%w: tokens %d exceeds max %d", ErrBudgetExceeded, next.UsedTokens, limits.MaxTokens)
	case next.UsedModelCalls > limits.MaxModelCalls:
		return Usage{}, fmt.Errorf("%w: model calls %d exceeds max %d", ErrBudgetExceeded, next.UsedModelCalls, limits.MaxModelCalls)
	case next.UsedWallTimeMS > limits.MaxWallTimeMS:
		return Usage{}, fmt.Errorf("%w: wall time %dms exceeds max %dms", ErrBudgetExceeded, next.UsedWallTimeMS, limits.MaxWallTimeMS)
	case next.Depth > limits.MaxDepth:
		return Usage{}, fmt.Errorf("%w: depth %d exceeds max %d", ErrBudgetExceeded, next.Depth, limits.MaxDepth)
	case next.UsedRetries > limits.MaxRetries:
		return Usage{}, fmt.Errorf("%w: retries %d exceeds max %d", ErrBudgetExceeded, next.UsedRetries, limits.MaxRetries)
	case next.UsedSubagents > limits.MaxSubagents:
		return Usage{}, fmt.Errorf("%w: subagents %d exceeds max %d", ErrBudgetExceeded, next.UsedSubagents, limits.MaxSubagents)
	}
	return next, nil
}

type Budget struct {
	ID             int64
	OrganizationID string
	RootTaskID     int64
	TaskID         int64
	RoleID         string
	ParentBudgetID *int64
	Limits         Limits
	Usage          Usage
	Version        int64
}
