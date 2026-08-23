package modelruntime

import (
	"strings"
	"testing"
)

// The helper every adapter uses to describe an incomplete read must produce
// an outcome the validator accepts. These two halves live in different files
// and came apart: the helper set an HTTP status and a partial-body hash,
// while the validator insisted an ambiguous outcome had neither. Every
// incomplete read on all four HTTP adapters was rejected as an invalid
// request, so the one outcome that most needs to be durable -- the call may
// already have been billed -- was the one that could not be written down.
func TestAnIncompleteReadCanBeRecorded(t *testing.T) {
	outcome := IncompleteReadOutcome(200, "req-abc", "3c7f5055b0a01835eeaf933c33905a56d410daa8e4a72732ac6606ea47d0faea", "v1")
	if outcome.OutcomeClassification != ProviderOutcomeAmbiguous {
		t.Fatalf("an incomplete read is ambiguous, got %q", outcome.OutcomeClassification)
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("the outcome every adapter builds must be recordable: %v", err)
	}
}

// Widening the rule must not make ambiguous a place to put anything.
func TestAmbiguousStillRefusesWhatWouldMakeItMeaningless(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*ProviderOutcome)
		wantMsg string
	}{
		{
			// Without a code there is nothing to reconcile against and
			// nothing for an operator to act on.
			name:    "no error code",
			mutate:  func(o *ProviderOutcome) { o.ErrorCode = "" },
			wantMsg: "ambiguous provider outcome is inconsistent",
		},
		{
			// A confirmed cancellation is knowledge, and knowing is the
			// opposite of this classification.
			name:    "claims a confirmed cancellation",
			mutate:  func(o *ProviderOutcome) { o.CancellationConfirmed = true },
			wantMsg: "ambiguous provider outcome is inconsistent",
		},
		{
			name:    "impossible HTTP status",
			mutate:  func(o *ProviderOutcome) { o.HTTPStatus = 42 },
			wantMsg: "impossible status",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := IncompleteReadOutcome(200, "req-abc", "3c7f5055b0a01835eeaf933c33905a56d410daa8e4a72732ac6606ea47d0faea", "v1")
			tc.mutate(&outcome)
			err := outcome.Validate()
			if err == nil {
				t.Fatal("expected the outcome to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("want %q in %v", tc.wantMsg, err)
			}
		})
	}
}

// A request that never left still learned nothing, and that stays strict.
func TestNotSentIsStillTheOutcomeThatLearnedNothing(t *testing.T) {
	outcome := ProviderOutcome{
		OutcomeClassification: ProviderOutcomeNotSent,
		ErrorClass:            "transport",
		ErrorCode:             "dial_failed",
		HTTPStatus:            200,
	}
	if err := outcome.Validate(); err == nil {
		t.Fatal("a not-sent outcome may not carry an HTTP status")
	}
}
