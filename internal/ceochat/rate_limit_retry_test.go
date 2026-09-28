package ceochat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type scriptedExecutor struct {
	err error
}

func (s scriptedExecutor) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	return executionharness.ModelResult{FinalOutput: "ok"}, s.err
}

func rateLimitError() error {
	return executionharness.WithInvocationRef("938", &modelruntime.AdapterError{
		Phase:   modelruntime.AdapterFailureResponseReceived,
		Outcome: modelruntime.ProviderOutcome{HTTPStatus: 429, ErrorClass: "tokens", ErrorCode: "rate_limit_exceeded", Retryable: true},
	})
}

func retrying(errs []error) (rateLimitRetryExecutor, *[]int, *[]time.Duration) {
	var ordinals []int
	var slept []time.Duration
	return rateLimitRetryExecutor{
		build: func(ordinal int) (executionharness.ModelExecutor, error) {
			ordinals = append(ordinals, ordinal)
			return scriptedExecutor{err: errs[ordinal]}, nil
		},
		waits: []time.Duration{30 * time.Second, 60 * time.Second},
		sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	}, &ordinals, &slept
}

// Local smoke #43: a rate-limited CEO call ended the turn before it proposed anything.
func TestARateLimitedCEOCallIsRetriedAsANewInvocation(t *testing.T) {
	executor, ordinals, slept := retrying([]error{rateLimitError(), nil})
	result, err := executor.Invoke(context.Background(), executionharness.RunIdentity{}, executionharness.NormalizedModelRequest{})
	if err != nil || result.FinalOutput != "ok" {
		t.Fatalf("result %+v err %v", result, err)
	}
	if len(*ordinals) != 2 || (*ordinals)[1] != 1 || len(*slept) != 1 || (*slept)[0] != 30*time.Second {
		t.Fatalf("ordinals %v slept %v: want the call, a 30s pause, then retry ordinal 1", *ordinals, *slept)
	}
}

func TestRateLimitRetriesAreBounded(t *testing.T) {
	executor, ordinals, _ := retrying([]error{rateLimitError(), rateLimitError(), rateLimitError(), nil})
	if _, err := executor.Invoke(context.Background(), executionharness.RunIdentity{}, executionharness.NormalizedModelRequest{}); !rateLimited(err) {
		t.Fatalf("err %v: after the last retry the rate-limit failure is returned", err)
	}
	if len(*ordinals) != 3 {
		t.Fatalf("ordinals %v: the call plus two retries", *ordinals)
	}
}

func TestOnlyRateLimitsAreRetried(t *testing.T) {
	other := &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived,
		Outcome: modelruntime.ProviderOutcome{HTTPStatus: 500, ErrorCode: "server_error", Retryable: true}}
	for name, err := range map[string]error{
		"a server error":      other,
		"an unclassified one": errors.New("boom"),
		"an incomplete answer": &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived,
			Outcome: modelruntime.ProviderOutcome{HTTPStatus: 200, ErrorCode: "response_incomplete_max_output_tokens"}},
	} {
		executor, ordinals, _ := retrying([]error{err, nil})
		if _, got := executor.Invoke(context.Background(), executionharness.RunIdentity{}, executionharness.NormalizedModelRequest{}); got == nil || len(*ordinals) != 1 {
			t.Errorf("%s: retried (%v, %v)", name, got, *ordinals)
		}
	}
}
