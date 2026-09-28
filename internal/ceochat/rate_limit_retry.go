package ceochat

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// A chat turn is one harness run of up to MaxTurns model calls, and its task has one attempt: a
// failed model call ends the turn. The CEO model sends ~96K input tokens a call, and three calls in
// half a minute exceeded the provider's tokens-per-minute limit (local smokes #42 and #43, HTTP 429
// rate_limit_exceeded): #43 ended before it could propose anything. A call the provider refused as
// rate limited is retried after a pause, as a new invocation, a bounded number of times. Nothing
// else is retried: any other failure still ends the turn.

// rateLimitRetryWaits are the pauses before each retry; their count bounds the retries.
var rateLimitRetryWaits = []time.Duration{30 * time.Second, 60 * time.Second}

type rateLimitRetryExecutor struct {
	// build returns the executor for a retry ordinal (0 is the call itself).
	build func(ordinal int) (executionharness.ModelExecutor, error)
	waits []time.Duration
	sleep func(context.Context, time.Duration) error
}

func (r rateLimitRetryExecutor) Invoke(ctx context.Context, identity executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	for ordinal := 0; ; ordinal++ {
		executor, err := r.build(ordinal)
		if err != nil {
			return executionharness.ModelResult{}, err
		}
		result, err := executor.Invoke(ctx, identity, request)
		if err == nil || ordinal >= len(r.waits) || !rateLimited(err) {
			return result, err
		}
		if sleepErr := r.sleep(ctx, r.waits[ordinal]); sleepErr != nil {
			return result, err
		}
	}
}

// rateLimited reports a provider refusal for rate: a retryable answer with HTTP 429 or a
// rate-limit error code.
func rateLimited(err error) bool {
	adapterErr, ok := modelruntime.AsAdapterError(err)
	if !ok || adapterErr.Phase != modelruntime.AdapterFailureResponseReceived || !adapterErr.Outcome.Retryable {
		return false
	}
	return adapterErr.Outcome.HTTPStatus == http.StatusTooManyRequests ||
		strings.Contains(strings.ToLower(adapterErr.Outcome.ErrorCode), "rate_limit")
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
