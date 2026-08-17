package executive

import "errors"

var (
	ErrInvalidInput               = errors.New("executive invalid input")
	ErrContractRejected           = errors.New("executive model result contract rejected")
	ErrForbiddenField             = errors.New("executive forbidden model-controlled field")
	ErrPlanTooLarge               = errors.New("executive plan exceeds configured bounds")
	ErrRegistryMismatch           = errors.New("executive organization registry mismatch")
	ErrRoleNotAssignable          = errors.New("executive role is not assignable")
	ErrCrossDepartmentDelegation  = errors.New("executive cross-department delegation rejected")
	ErrDependencyCycle            = errors.New("executive dependency cycle")
	ErrBudgetExceeded             = errors.New("executive invocation budget exceeded")
	ErrDispatchAssignmentRequired = errors.New("executive dispatch assignment required")
	ErrModelOutcomeAmbiguous      = errors.New("executive model outcome ambiguous")
	ErrToolIntentRejected         = errors.New("executive tool intent rejected")
	ErrCompletionFailed           = errors.New("executive completion verification failed")
	ErrCompletionInconclusive     = errors.New("executive completion verification inconclusive")
	ErrRunBlocked                 = errors.New("executive run blocked")
	ErrRunTerminal                = errors.New("executive run is terminal")

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
)
