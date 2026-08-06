package evaluation

import (
	"context"
	"time"
)

// TraceSource resolves an opaque TraceRef to its materialized trace. The
// Branch 14 decision-graph ledger is the eventual real implementation; until
// that merges, only fakes exist.
type TraceSource interface {
	LoadTrace(context.Context, TraceRef) (EvaluationTrace, error)
}

// Evaluator scores a single case, for either the baseline or the candidate,
// against a materialized trace.
type Evaluator interface {
	Evaluate(context.Context, EvaluationRequest) (EvaluationResult, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
