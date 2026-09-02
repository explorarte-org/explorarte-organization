package modelruntime

import "errors"

var (
	ErrInvalidRequest           = errors.New("invalid model runtime request")
	ErrNotFound                 = errors.New("model runtime entity not found")
	ErrConflict                 = errors.New("model runtime conflict")
	ErrDisabled                 = errors.New("model runtime dispatch disabled")
	ErrProviderUnavailable      = errors.New("model provider adapter unavailable")
	ErrBindingNotFound          = errors.New("model binding not found")
	ErrCapabilityMismatch       = errors.New("model capability mismatch")
	ErrAuthorizationDenied      = errors.New("model invocation authorization denied")
	ErrEgressPolicyUnpinned     = errors.New("model egress policy unpinned")
	ErrEgressDenied             = errors.New("model egress denied")
	ErrEgressEvaluation         = errors.New("model egress evaluation error")
	ErrContextRejected          = errors.New("model invocation context rejected")
	ErrModelInputSecretRejected = errors.New("model invocation input rejected credential material")
	ErrTaskAttemptRejected      = errors.New("model invocation task attempt rejected")
	ErrClaimUnavailable         = errors.New("model invocation claim unavailable")
	ErrConcurrencyLimit         = errors.New("model runtime global concurrency limit reached")
	ErrClaimTokenMismatch       = errors.New("model dispatch claim token mismatch")
	ErrAmbiguousOutcome         = errors.New("ambiguous external model outcome")
	ErrResponseRejected         = errors.New("model response rejected")
	ErrCancellationRequested    = errors.New("model invocation cancellation requested")
	ErrDatabaseUnavailable      = errors.New("model runtime database unavailable")
	// ErrProviderWalletNotProvisioned is returned when a provider is
	// priced and routed but has no provider_wallets row at all (G2-001) --
	// distinct from a real budget/balance exhaustion, which is a funded
	// wallet that simply ran out, not a forgotten provisioning step.
	ErrProviderWalletNotProvisioned = errors.New("model runtime: provider wallet not provisioned")

	ErrAssignmentUnavailable        = errors.New("model dispatcher assignment unavailable")
	ErrAssignmentRevisionDrift      = errors.New("model dispatcher assignment organization revision drift")
	ErrAssignmentVigencyExpired     = errors.New("model dispatcher assignment vigency expired")
	ErrAssignmentQuotaExhausted     = errors.New("model dispatcher assignment quota exhausted")
	ErrExecutionPrincipalDisabled   = errors.New("model execution principal disabled")
	ErrExecutionPrincipalMismatch   = errors.New("model execution principal mismatch")
	ErrDispatcherAssignmentUnpinned = errors.New("model invocation has no pinned dispatcher assignment")
	ErrExecutionIdentityUnpinned    = errors.New("execution identity policy is not pinned")
	ErrExecutionIdentityDenied      = errors.New("execution identity assertion denied")

	// ErrContextTokenTelemetryBindingMismatch is returned when the supplied
	// ExecutionContextView does not belong to the invocation's own
	// context_snapshot_id (M1.2 section 11). A mismatched binding is never
	// persisted.
	ErrContextTokenTelemetryBindingMismatch = errors.New("context token telemetry: execution context view does not belong to the invocation's context snapshot")
	// ErrContextTokenTelemetryContradiction is returned when a second
	// RecordContextTokenTelemetry call for an invocation that already has
	// durable M1.2 telemetry supplies different facts. The original record
	// is never silently overwritten.
	ErrContextTokenTelemetryContradiction = errors.New("context token telemetry: contradictory telemetry was already recorded for this invocation")
)

// ErrProviderOutcomeUnknown reports that no provider outcome was recorded for
// an invocation, so nothing is known about whether its failure was transient.
//
// It exists because "no answer" and "the answer is no" were the same value.
// The Executive's own comment said an unreadable answer means no retry, and
// it could not apply that rule: a missing row and a recorded false arrived
// identically, so a run that had never been asked about looked exactly like
// one that had been judged permanent.
var ErrProviderOutcomeUnknown = errors.New("model runtime: no provider outcome recorded for invocation")
