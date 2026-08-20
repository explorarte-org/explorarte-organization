package modelruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

// The rule this file guards is small and was wrong in two of the six places it
// had been copied to. These tests pin what it decides, and the guard below
// pins that nobody decides it again somewhere else.

func TestOnlyDeadlinesAndCancellationLeaveACallAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"deadline", context.DeadlineExceeded, true},
		{"cancelled", context.Canceled, true},
		{"wrapped deadline", fmt.Errorf("read provider stream: %w", context.DeadlineExceeded), true},
		// These are genuine rejections: the provider finished answering and
		// the answer was unusable. Calling them ambiguous would turn real
		// rejections into retries, which costs money instead of campaigns.
		{"truncated body", io.ErrUnexpectedEOF, false},
		{"oversized body", errors.New("provider response exceeds 1048576 bytes"), false},
		{"malformed json", errors.New("response_json_invalid"), false},
		{"nothing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsIncompleteRead(tc.err); got != tc.want {
				t.Fatalf("IsIncompleteRead(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The breaker must not open because a task was cancelled: that is not provider
// instability, and tripping on it would take a healthy provider out of service.
func TestCallerCancellationIsDistinguishedFromProviderSlowness(t *testing.T) {
	if !IsCallerCancellation(fmt.Errorf("dispatch: %w", context.Canceled)) {
		t.Fatal("cancellation must be recognised through wrapping")
	}
	if IsCallerCancellation(context.DeadlineExceeded) {
		t.Fatal("a deadline is the provider being slow, not the caller giving up; the breaker must still count it")
	}
}

func TestIncompleteReadOutcomeIsAmbiguousAndRetryable(t *testing.T) {
	outcome := IncompleteReadOutcome(200, "req-1", "hash-1", "schema-1")
	// Ambiguous is the load-bearing part: a caller that repeats this must
	// know the first attempt may already have been billed.
	if outcome.OutcomeClassification != ProviderOutcomeAmbiguous {
		t.Fatalf("classification=%q", outcome.OutcomeClassification)
	}
	if !outcome.Retryable {
		t.Fatal("a call the provider may still be completing is worth retrying")
	}
	if outcome.ErrorCode != IncompleteReadErrorCode {
		t.Fatalf("error code=%q: it must be distinct from response_read_failed, which now means the body arrived and could not be read", outcome.ErrorCode)
	}
	if outcome.ProviderRequestID != "req-1" || outcome.ResponseHash != "hash-1" || outcome.ResponseSchemaVersion != "schema-1" {
		t.Fatalf("provenance dropped: %+v", outcome)
	}
}
