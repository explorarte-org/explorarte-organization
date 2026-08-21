package executive

import "errors"

var (
	ErrInvalidInput     = errors.New("executive invalid input")
	ErrContractRejected = errors.New("executive model result contract rejected")
	// ErrModelResultContractRejected is the specific sentinel for the case
	// where the provider succeeded (durable invocation = succeeded) but the
	// host-side semantic validation rejected the result. This is distinct
	// from the generic ErrContractRejected which can occur at many other
	// boundaries. This sentinel is non-blocking (retryable) because a fresh
	// attempt may produce a valid result.
	ErrModelResultContractRejected = errors.New("executive model result contract rejected after provider success")
	ErrForbiddenField              = errors.New("executive forbidden model-controlled field")
	// ErrDesignIdentityMismatch means an adjudication result names a design
	// other than the one it was handed. It is deliberately its own sentinel:
	// a mismatch here is not a malformed contract, it is a correct-looking
	// verdict about the wrong artifact, and freeze must never see it.
	ErrDesignIdentityMismatch = errors.New("executive adjudication design identity mismatch")
	ErrPlanTooLarge           = errors.New("executive plan exceeds configured bounds")
	ErrRegistryMismatch       = errors.New("executive organization registry mismatch")
	ErrRoleNotAssignable      = errors.New("executive role is not assignable")
	// ErrRequirementUnsatisfiable means a plan attached a blocking obligation
	// to an executor that has no way to discharge it. The contract is
	// well-formed; the topology is impossible.
	ErrRequirementUnsatisfiable   = errors.New("executive required requirement has no satisfier in the receiving execution path")
	ErrCrossDepartmentDelegation  = errors.New("executive cross-department delegation rejected")
	ErrDependencyCycle            = errors.New("executive dependency cycle")
	ErrBudgetExceeded             = errors.New("executive invocation budget exceeded")
	ErrDispatchAssignmentRequired = errors.New("executive dispatch assignment required")

	// ErrMissionRejected marks a mission the task engine refused because the
	// request itself was malformed -- not because anything was unavailable.
	//
	// The distinction is the whole point. An unavailable dependency is worth
	// coming back for; a rejected request is not, because the next attempt
	// submits the same policy and the same plan and is refused for the same
	// reason. Without it a deterministic refusal reads as a transient one,
	// and the worker retries it until somebody notices -- which took eight
	// hours and about nine thousand six hundred attempts the first time,
	// silently, on a campaign whose design had already frozen.
	ErrMissionRejected        = errors.New("executive engineering mission rejected")
	ErrModelOutcomeAmbiguous  = errors.New("executive model outcome ambiguous")
	ErrToolIntentRejected     = errors.New("executive tool intent rejected")
	ErrCompletionFailed       = errors.New("executive completion verification failed")
	ErrCompletionInconclusive = errors.New("executive completion verification inconclusive")
	ErrRunBlocked             = errors.New("executive run blocked")
	ErrRunTerminal            = errors.New("executive run is terminal")

	// ErrExecutionPrincipalUnavailable means the canonical role-bound
	// principal could not be consulted. It is not a statement about the
	// principal: the attempt is left alone and the same work is retried later.
	ErrExecutionPrincipalUnavailable = errors.New("executive role-bound execution principal unavailable")
	// ErrExecutionPrincipalUnusable means the role's execution identity was
	// consulted and cannot be used -- disabled, revoked, or bound to another
	// role or organization. It fails closed; re-creating a revoked identity is
	// never the Executive's call.
	ErrExecutionPrincipalUnusable = errors.New("executive role-bound execution principal is not usable")
	// ErrLeaseLost means the lease keeper could not keep this attempt's lease
	// alive, so nothing this run produced may be recorded under it. The task
	// engine's own expiry/reconcile path creates the next attempt.
	ErrLeaseLost = errors.New("executive task lease was lost during execution")
	// ErrExecutionAuthorityUnavailable means Harness authority could not be
	// evaluated. The run was left resumable and is not a denial.
	ErrExecutionAuthorityUnavailable = errors.New("executive execution authority unavailable")
	// ErrExecutionAuthorityDenied means authority was evaluated and refused.
	ErrExecutionAuthorityDenied = errors.New("executive execution authority denied")
	// ErrIndeterminateToolExecution means a tool may or may not have produced
	// an external side effect and nobody can tell. It is terminal by design:
	// re-running it is the one failure mode that reaches outside the system.
	ErrIndeterminateToolExecution = errors.New("executive indeterminate tool execution requires reconciliation")
	// ErrExecutionInterrupted means the run stopped without a verdict because
	// its context was cancelled. Nothing durable changed.
	ErrExecutionInterrupted = errors.New("executive execution interrupted")
	// ErrHarnessHistoryFailed means the durable Harness history could not be
	// read or extended, so the trajectory cannot be trusted.
	ErrHarnessHistoryFailed = errors.New("executive harness execution history failed")
	// ErrRunIdentityDrift means the durable run identity no longer matches the
	// run being entered.
	ErrRunIdentityDrift = errors.New("executive harness run identity drift")
	// ErrPriorExecutionUnresolved means an earlier execution of this task may
	// already have reached the provider and has no resolved outcome yet. No new
	// execution may begin beside it; Model Runtime's reconciliation decides
	// what that earlier call was, and until it does, the correct behavior is to
	// wait rather than to guess.
	ErrPriorExecutionUnresolved = errors.New("executive prior model execution is unresolved at the provider boundary")
)
