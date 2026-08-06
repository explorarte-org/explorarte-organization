package modelruntime

import "errors"

var (
	ErrInvalidRequest        = errors.New("invalid model runtime request")
	ErrNotFound              = errors.New("model runtime entity not found")
	ErrConflict              = errors.New("model runtime conflict")
	ErrDisabled              = errors.New("model runtime dispatch disabled")
	ErrProviderUnavailable   = errors.New("model provider adapter unavailable")
	ErrBindingNotFound       = errors.New("model binding not found")
	ErrCapabilityMismatch    = errors.New("model capability mismatch")
	ErrAuthorizationDenied   = errors.New("model invocation authorization denied")
	ErrEgressPolicyUnpinned  = errors.New("model egress policy unpinned")
	ErrEgressDenied          = errors.New("model egress denied")
	ErrEgressEvaluation      = errors.New("model egress evaluation error")
	ErrContextRejected       = errors.New("model invocation context rejected")
	ErrTaskAttemptRejected   = errors.New("model invocation task attempt rejected")
	ErrClaimUnavailable      = errors.New("model invocation claim unavailable")
	ErrConcurrencyLimit      = errors.New("model runtime global concurrency limit reached")
	ErrClaimTokenMismatch    = errors.New("model dispatch claim token mismatch")
	ErrAmbiguousOutcome      = errors.New("ambiguous external model outcome")
	ErrResponseRejected      = errors.New("model response rejected")
	ErrCancellationRequested = errors.New("model invocation cancellation requested")
	ErrDatabaseUnavailable   = errors.New("model runtime database unavailable")
)
