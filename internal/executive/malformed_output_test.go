package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// See malformed_output.go. Local smoke #26 (root 1601): the planner's JSON closed one object twice,
// the task had three attempts, and the root was blocked after the first.

const root1601Reason = "model execution failed: model response rejected: invalid JSON response"

func TestAMalformedModelResponseIsRetriedWithACorrection(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureModelError
	fixture.harness.invocationStatus = "failed"
	fixture.harness.invocationErrorCode = normalizationFailedErrorCode
	fixture.harness.terminationReason = root1601Reason

	_, err := fixture.drive(t)
	if !errors.Is(err, ErrTaskRetryScheduled) || !isNonBlockingPhaseError(err) {
		t.Fatalf("a malformed response = %v, want a retry that does not block the root", err)
	}
	if len(fixture.tasks.failed) != 1 || fixture.tasks.failed[0] != "model_output_malformed" {
		t.Fatalf("attempt failures=%v", fixture.tasks.failed)
	}
	task, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if task.Status != "retry_wait" {
		t.Fatalf("task status=%s, want retry_wait", task.Status)
	}
	summary := task.Attempts[len(task.Attempts)-1].ResultSummary
	for _, want := range []string{"invalid JSON response", "return exactly one JSON object that parses", "every brace and bracket closed once"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the next attempt is not told %q: %s", want, summary)
		}
	}
}

func TestAHostSideNormalizationFailureStaysTerminal(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureModelError
	fixture.harness.invocationStatus = "failed"
	fixture.harness.invocationErrorCode = normalizationFailedErrorCode
	fixture.harness.terminationReason = "model execution failed: model response rejected: invalid stored schema"
	if _, err := fixture.drive(t); !errors.Is(err, ErrCompletionFailed) || errors.Is(err, ErrTaskRetryScheduled) {
		t.Fatalf("a host-side refusal = %v, want the terminal failure it always was", err)
	}
	if len(fixture.tasks.failed) != 1 || fixture.tasks.failed[0] != "model_invocation_failed" {
		t.Fatalf("attempt failures=%v", fixture.tasks.failed)
	}
}

func TestOnlyTheModelsOwnOutputDefectsAreRetried(t *testing.T) {
	for reason, want := range map[string]bool{
		"model response rejected: invalid JSON response":                  true,
		"model response rejected: decode JSON response":                   true,
		"model response rejected: schema mismatch: missing field":         true,
		"model response rejected: response exceeds byte limit":            true,
		"model response rejected: normalized response exceeds byte limit": true,
		"model response rejected: text output is not UTF-8":               true,
		"model response rejected: invalid stored schema":                  false,
		"model response rejected: stored schema is not an object":         false,
		"model response rejected: invalid response limits":                false,
		"model response rejected: hash normalized response":               false,
		"model response rejected: unsupported output mode":                false,
	} {
		if got := modelOutputDefect(normalizationFailedErrorCode, reason); got != want {
			t.Errorf("modelOutputDefect(%q) = %v, want %v", reason, got, want)
		}
	}
	if modelOutputDefect("provider_http_error", root1601Reason) {
		t.Error("a failure that is not a normalization refusal was classified as a model output defect")
	}
}

// Local smoke #31 (root 1701): a design worker reasoned through its whole output budget and wrote
// nothing; the attempt was failed as not retryable with two attempts left.
const root1701Reason = "model execution failed: model provider adapter error: response_received: response: response_truncated_empty"

func TestAResponseTruncatedBeforeAnyAnswerIsRetriedWithACorrection(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureModelError
	fixture.harness.invocationStatus = "failed"
	fixture.harness.invocationErrorCode = truncatedEmptyErrorCode
	fixture.harness.terminationReason = root1701Reason

	_, err := fixture.drive(t)
	if !errors.Is(err, ErrTaskRetryScheduled) || !isNonBlockingPhaseError(err) {
		t.Fatalf("a truncated empty response = %v, want a retry that does not block the root", err)
	}
	task, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if task.Status != "retry_wait" {
		t.Fatalf("task status=%s, want retry_wait", task.Status)
	}
	summary := task.Attempts[len(task.Attempts)-1].ResultSummary
	for _, want := range []string{"response_truncated_empty", "reason briefly, then write the JSON answer"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the next attempt is not told %q: %s", want, summary)
		}
	}
}

func TestOtherProviderFailuresGetNoOutputCorrection(t *testing.T) {
	for _, code := range []string{"response_content_filtered", "provider_http_error", "response_json_invalid", ""} {
		if _, ok := modelOutputCorrection(code, root1701Reason); ok {
			t.Errorf("%q was treated as the model's own output defect", code)
		}
	}
}
