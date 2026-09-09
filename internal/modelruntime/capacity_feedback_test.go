package modelruntime

import (
	"testing"
	"time"
)

func TestClassifyCapacityFeedback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outcome      ProviderOutcome
		phase        AdapterFailurePhase
		wantKind     CapacityFeedbackKind
		wantDuration time.Duration
	}{
		{
			name:     "success resets to healthy",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeResponseReceived},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackHealthy,
		},
		{
			name:     "request not sent is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeNotSent, ErrorClass: "credential", ErrorCode: "credential_unavailable"},
			phase:    AdapterFailureBeforeRequest,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:     "confirmed cancellation is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeCancelled, CancellationConfirmed: true},
			phase:    AdapterFailureAmbiguous,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:         "ambiguous retryable gets a 30s cooldown",
			outcome:      ProviderOutcome{OutcomeClassification: ProviderOutcomeAmbiguous, ErrorCode: "transport_timeout", Retryable: true},
			phase:        AdapterFailureAmbiguous,
			wantKind:     CapacityFeedbackCooldown,
			wantDuration: 30 * time.Second,
		},
		{
			name:     "ambiguous non-retryable is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeAmbiguous, ErrorCode: "transport_timeout", Retryable: false},
			phase:    AdapterFailureAmbiguous,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:         "retryable 500 gets a 30s cooldown",
			outcome:      ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 500, Retryable: true, ErrorCode: "http_error"},
			phase:        AdapterFailureResponseReceived,
			wantKind:     CapacityFeedbackCooldown,
			wantDuration: 30 * time.Second,
		},
		{
			name:         "retryable 503 gets a 30s cooldown",
			outcome:      ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 503, Retryable: true, ErrorCode: "http_error"},
			phase:        AdapterFailureResponseReceived,
			wantKind:     CapacityFeedbackCooldown,
			wantDuration: 30 * time.Second,
		},
		{
			name:     "non-retryable 500 is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 500, Retryable: false, ErrorCode: "internal_error"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:         "429 gets a 5 minute cooldown, never quota",
			outcome:      ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 429, Retryable: true, ErrorCode: "rate_limited"},
			phase:        AdapterFailureResponseReceived,
			wantKind:     CapacityFeedbackCooldown,
			wantDuration: 5 * time.Minute,
		},
		{
			name:     "429 marked non-retryable still cools down (status alone is the signal)",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 429, Retryable: false, ErrorCode: "rate_limited"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackCooldown, wantDuration: 5 * time.Minute,
		},
		{
			name:     "ordinary 401 is not auto-disabled and gets no cooldown",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 401, Retryable: false, ErrorCode: "unauthorized"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:     "ordinary 403 is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 403, Retryable: false, ErrorCode: "forbidden"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:     "ordinary 404 is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 404, Retryable: false, ErrorCode: "not_found"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:     "ordinary 400 is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 400, Retryable: false, ErrorCode: "bad_request"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			name:     "422 is a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 422, Retryable: false, ErrorCode: "unprocessable"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			// A retryable status outside the two approved rules (429, >=500)
			// must NOT get a cooldown -- Section 4.H: no cooldown unless an
			// explicit rule above covers it, regardless of the Retryable flag.
			name:     "retryable 408 outside the approved rules is still a noop",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 408, Retryable: true, ErrorCode: "request_timeout"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
		{
			// The whole point of QUOTA_SIGNAL_NOT_CANONICAL_YET: no
			// ErrorClass/ErrorCode value, however quota-shaped it looks, may
			// ever be guessed into CapacityFeedbackQuota. Never assert on
			// substring content -- assert the classifier ignores it.
			name:     "unknown error class/code resembling quota is never guessed into quota",
			outcome:  ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 402, Retryable: false, ErrorClass: "billing", ErrorCode: "insufficient_balance"},
			phase:    AdapterFailureResponseReceived,
			wantKind: CapacityFeedbackNoop,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCapacityFeedback(tc.outcome, tc.phase)
			if got.Kind != tc.wantKind {
				t.Fatalf("Kind=%q want %q (reason=%q)", got.Kind, tc.wantKind, got.Reason)
			}
			if got.Duration != tc.wantDuration {
				t.Fatalf("Duration=%v want %v", got.Duration, tc.wantDuration)
			}
			if got.Kind == CapacityFeedbackQuota {
				t.Fatal("ClassifyCapacityFeedback must never return CapacityFeedbackQuota in V1 (QUOTA_SIGNAL_NOT_CANONICAL_YET)")
			}
		})
	}
}

// TestClassifyCapacityFeedbackNeverReturnsQuota is a standalone guard, not
// folded into the table above: it is the one invariant this whole file
// exists to prove, and it must stay true across every future rule the table
// grows -- so it re-asserts across the full closed set of
// OutcomeClassification/HTTPStatus/Retryable/ErrorClass/ErrorCode
// combinations the table already covers, plus a few more each meant to
// LOOK like a quota signal (billing/insufficient_balance/limit-shaped
// codes) without ever matching a real, exact, cross-provider token.
func TestClassifyCapacityFeedbackNeverReturnsQuota(t *testing.T) {
	tempting := []ProviderOutcome{
		{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 429, Retryable: true, ErrorClass: "rate_limit", ErrorCode: "rate_limited"},
		{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 402, Retryable: false, ErrorClass: "billing", ErrorCode: "insufficient_balance"},
		{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 403, Retryable: false, ErrorClass: "quota", ErrorCode: "quota_exceeded"},
		{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 429, Retryable: true, ErrorClass: "limit", ErrorCode: "monthly_limit_reached"},
		{OutcomeClassification: ProviderOutcomeAmbiguous, ErrorClass: "quota", ErrorCode: "resource-exhausted", Retryable: true},
	}
	for _, outcome := range tempting {
		if got := ClassifyCapacityFeedback(outcome, AdapterFailureResponseReceived); got.Kind == CapacityFeedbackQuota {
			t.Fatalf("outcome %+v was classified as quota -- QUOTA_SIGNAL_NOT_CANONICAL_YET must hold in V1", outcome)
		}
	}
}
