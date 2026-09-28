package modelruntimeadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type scriptedExecutor struct{ err error }

func (s scriptedExecutor) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	return executionharness.ModelResult{FinalOutput: "ok"}, s.err
}

func providerRefusal(status int, code string, retryable bool) error {
	return executionharness.WithInvocationRef("938", &modelruntime.AdapterError{
		Phase:   modelruntime.AdapterFailureResponseReceived,
		Outcome: modelruntime.ProviderOutcome{HTTPStatus: status, ErrorCode: code, Retryable: retryable},
	})
}

func retrying(errs []error) (TransientRetryExecutor, *[]int, *[]time.Duration) {
	var ordinals []int
	var slept []time.Duration
	return TransientRetryExecutor{
		Build: func(ordinal int) (executionharness.ModelExecutor, error) {
			ordinals = append(ordinals, ordinal)
			return scriptedExecutor{err: errs[ordinal]}, nil
		},
		Waits: []time.Duration{30 * time.Second, 60 * time.Second},
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	}, &ordinals, &slept
}

// Local smokes #43 (CEO chat, HTTP 429) and #48 (finance review, HTTP 503): one transient refusal
// ended the run. It is retried after a pause as a new invocation.
func TestATransientProviderRefusalIsRetriedAsANewInvocation(t *testing.T) {
	for name, refusal := range map[string]error{
		"rate limited": providerRefusal(429, "rate_limit_exceeded", true),
		"unavailable":  providerRefusal(503, "http_error", true),
	} {
		executor, ordinals, slept := retrying([]error{refusal, nil})
		result, err := executor.Invoke(context.Background(), executionharness.RunIdentity{}, executionharness.NormalizedModelRequest{})
		if err != nil || result.FinalOutput != "ok" || len(*ordinals) != 2 || (*ordinals)[1] != 1 || len(*slept) != 1 || (*slept)[0] != 30*time.Second {
			t.Errorf("%s: result %+v err %v ordinals %v slept %v", name, result, err, *ordinals, *slept)
		}
	}
}

func TestTransientRetriesAreBounded(t *testing.T) {
	refusal := providerRefusal(503, "http_error", true)
	executor, ordinals, _ := retrying([]error{refusal, refusal, refusal, nil})
	if _, err := executor.Invoke(context.Background(), executionharness.RunIdentity{}, executionharness.NormalizedModelRequest{}); !TransientProviderFailure(err) {
		t.Fatalf("err %v: after the last retry the refusal is returned", err)
	}
	if len(*ordinals) != 3 || len(DefaultTransientRetryWaits) != 2 {
		t.Fatalf("ordinals %v: the call plus two retries", *ordinals)
	}
}

func TestOnlyTransientRefusalsAreRetried(t *testing.T) {
	for name, err := range map[string]error{
		"a bad request":              providerRefusal(400, "invalid_request", false),
		"a 503 not marked retryable": providerRefusal(503, "http_error", false),
		"an unclassified error":      errors.New("boom"),
		"an incomplete answer":       providerRefusal(200, "response_incomplete_max_output_tokens", false),
	} {
		executor, ordinals, _ := retrying([]error{err, nil})
		if _, got := executor.Invoke(context.Background(), executionharness.RunIdentity{}, executionharness.NormalizedModelRequest{}); got == nil || len(*ordinals) != 1 {
			t.Errorf("%s: retried (%v, %v)", name, got, *ordinals)
		}
	}
}
