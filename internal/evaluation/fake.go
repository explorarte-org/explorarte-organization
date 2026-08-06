package evaluation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeTraceSource is an in-memory TraceSource for tests. It is safe for
// concurrent use.
type FakeTraceSource struct {
	mu     sync.Mutex
	traces map[TraceRef]EvaluationTrace
	err    error
}

func NewFakeTraceSource() *FakeTraceSource {
	return &FakeTraceSource{traces: make(map[TraceRef]EvaluationTrace)}
}

// Seed registers a trace so LoadTrace can resolve its ref. Payload is cloned
// so a caller mutating its own slice after seeding, or mutating a slice
// returned by LoadTrace, can never corrupt what other callers observe.
func (f *FakeTraceSource) Seed(trace EvaluationTrace) {
	f.mu.Lock()
	defer f.mu.Unlock()
	trace.Payload = cloneBytes(trace.Payload)
	f.traces[trace.Ref] = trace
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// SetError makes every subsequent LoadTrace call fail with err.
func (f *FakeTraceSource) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *FakeTraceSource) LoadTrace(ctx context.Context, ref TraceRef) (EvaluationTrace, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationTrace{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return EvaluationTrace{}, f.err
	}
	trace, ok := f.traces[ref]
	if !ok {
		return EvaluationTrace{}, fmt.Errorf("%w: no trace seeded for run %d", ErrInvalidTraceRef, ref.RunID)
	}
	trace.Payload = cloneBytes(trace.Payload)
	return trace, nil
}

// FakeEvaluator is a deterministic Evaluator for tests. Without a custom
// ScoreFunc it reports a single "payload_length" metric derived from the
// trace payload and always verdicts pass, which is enough for baseline and
// candidate traces of different lengths to produce different metric values.
type FakeEvaluator struct {
	mu        sync.Mutex
	ScoreFunc func(EvaluationRequest) (EvaluationResult, error)
}

func NewFakeEvaluator() *FakeEvaluator { return &FakeEvaluator{} }

func (f *FakeEvaluator) SetScoreFunc(fn func(EvaluationRequest) (EvaluationResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ScoreFunc = fn
}

func (f *FakeEvaluator) Evaluate(ctx context.Context, request EvaluationRequest) (EvaluationResult, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationResult{}, err
	}
	f.mu.Lock()
	fn := f.ScoreFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(request)
	}
	return EvaluationResult{
		CaseID:   request.Case.ID,
		Role:     request.Role,
		TraceRef: request.Trace.Ref,
		Metrics: []Metric{
			{Name: "payload_length", Value: float64(len(request.Trace.Payload)), Unit: "bytes"},
		},
		Verdict:     VerdictPass,
		EvaluatedAt: time.Now().UTC(),
	}, nil
}
