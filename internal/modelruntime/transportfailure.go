package modelruntime

import (
	"context"
	"errors"
)

// What a failed response-body read MEANS is one decision, and it was written
// six times.
//
// Every HTTP adapter reached the same branch -- the request was sent, the
// provider answered with headers, and then reading the body failed -- and each
// one independently called it a clean provider rejection: not retryable, safe
// to treat as if nothing had been sent. That is wrong in the one case that
// matters most. A timeout or a cancellation while the body is still arriving
// says nothing about whether the provider accepted the work; it very likely
// did, and may finish it and bill for it.
//
// The cost of getting it wrong is not theoretical. AUTONOMY-SMOKE-001 lost a
// campaign to it twice: once when xAI held a queued request open past the
// client's patience, and once when a department plan outran DeepSeek's 180s
// ceiling. Both were transient, both were recorded as permanent rejections,
// and both ended the run.
//
// Six copies of a rule is six chances for one of them to be wrong, and two of
// them were. The decision lives here now, once, and the adapters ask.

// IncompleteReadErrorCode is the durable code for a response that the provider
// began sending and the client stopped receiving. It is deliberately distinct
// from response_read_failed, which now means what it says: the body arrived
// and could not be read.
const IncompleteReadErrorCode = "response_incomplete"

// IsIncompleteRead reports whether a body-read failure leaves the call
// AMBIGUOUS rather than rejected.
//
// Only deadlines and cancellation qualify. A malformed body, an oversized
// body, or a stream the provider ended badly are all genuine rejections: the
// provider finished answering and the answer was unusable. Widening this
// would turn real rejections into retries, which is the opposite mistake and
// costs money rather than campaigns.
func IsIncompleteRead(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// IsCallerCancellation distinguishes "we stopped waiting" from "the provider
// took too long".
//
// It exists because the circuit breaker must not open on the first: caller
// cancellation is not provider instability, and tripping the breaker on it
// would take a healthy provider out of service because a task was cancelled.
func IsCallerCancellation(err error) bool { return errors.Is(err, context.Canceled) }

// IncompleteReadOutcome is the single description of that state, so an
// operator reading two providers' failures reads the same shape.
func IncompleteReadOutcome(status int, providerRequestID, responseHash, schemaVersion string) ProviderOutcome {
	return ProviderOutcome{
		OutcomeClassification: ProviderOutcomeAmbiguous,
		ProviderRequestID:     providerRequestID,
		HTTPStatus:            status,
		ErrorClass:            "transport",
		ErrorCode:             IncompleteReadErrorCode,
		// Retryable, but the AMBIGUOUS classification is what actually
		// governs: a caller that repeats this must know the first attempt
		// may already have been billed.
		Retryable:             true,
		ResponseHash:          responseHash,
		ResponseSchemaVersion: schemaVersion,
	}
}
