package modelruntimeadapter

import (
	"context"
	"net/http"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// A run whose task has one attempt ends at its first failed model call, however transient. Two
// local smokes lost whole turns to answers a moment later would not have given: the CEO chat to
// HTTP 429 rate_limit_exceeded (#42, #43) and the finance review to HTTP 503 (#48).
// TransientRetryExecutor retries a call the provider refused transiently -- rate limited or
// unavailable, and marked retryable -- after a pause, as a new invocation (Config.RetryOrdinal), a
// bounded number of times. Every other failure is returned as it came.

// DefaultTransientRetryWaits are the pauses before each retry; their count bounds the retries. A
// caller's dispatch assignment must allow 1+len(waits) invocations per call.
var DefaultTransientRetryWaits = []time.Duration{30 * time.Second, 60 * time.Second}

type TransientRetryExecutor struct {
	// Build returns the executor for a retry ordinal; 0 is the call itself.
	Build func(ordinal int) (executionharness.ModelExecutor, error)
	Waits []time.Duration
	// Sleep pauses between attempts; nil sleeps on the real clock, honoring ctx.
	Sleep func(context.Context, time.Duration) error
}

var _ executionharness.ModelExecutor = TransientRetryExecutor{}

func (r TransientRetryExecutor) Invoke(ctx context.Context, identity executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	sleep := r.Sleep
	if sleep == nil {
		sleep = SleepContext
	}
	for ordinal := 0; ; ordinal++ {
		executor, err := r.Build(ordinal)
		if err != nil {
			return executionharness.ModelResult{}, err
		}
		result, err := executor.Invoke(ctx, identity, request)
		if err == nil || ordinal >= len(r.Waits) || !TransientProviderFailure(err) {
			return result, err
		}
		if sleepErr := sleep(ctx, r.Waits[ordinal]); sleepErr != nil {
			return result, err
		}
	}
}

// TransientProviderFailure reports a provider answer that refused the call for a reason a later
// call may not meet: marked retryable, with HTTP 429 or a 5xx status.
func TransientProviderFailure(err error) bool {
	adapterErr, ok := modelruntime.AsAdapterError(err)
	if !ok || adapterErr.Phase != modelruntime.AdapterFailureResponseReceived || !adapterErr.Outcome.Retryable {
		return false
	}
	status := adapterErr.Outcome.HTTPStatus
	return status == http.StatusTooManyRequests || status >= 500
}

func SleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
