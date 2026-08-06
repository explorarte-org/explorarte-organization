package cellworker

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// Dispatcher is the narrow port this package depends on. It matches
// modelruntime.DispatchService.Dispatch exactly (internal/modelruntime/dispatch_service.go),
// so the worker never needs the concrete adapter, egress, or identity wiring
// Rama 12 owns.
type Dispatcher interface {
	Dispatch(ctx context.Context, invocationID int64) (modelruntime.DispatchResult, error)
}

// WorkSource selects invocation IDs this execution principal is eligible to
// dispatch right now. The real, Postgres-backed implementation is wired
// after rebasing onto Rama 12; until then every caller uses a fake.
type WorkSource interface {
	ListEligible(ctx context.Context, principalKey string, limit int) ([]int64, error)
}

// Clock abstracts time so the poll/backoff loop is deterministically
// testable. Sleep returns false if it was interrupted by ctx.Done()
// instead of running to completion.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) bool
}

type systemClock struct{}

// SystemClock is the production Clock backed by real timers.
var SystemClock Clock = systemClock{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		// timer.C and ctx.Done() can both be ready at once; select picks
		// pseudo-randomly, so confirm ctx wasn't also cancelled instead of
		// trusting that this branch means "ran to completion".
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}
