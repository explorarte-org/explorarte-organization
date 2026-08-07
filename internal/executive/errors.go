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
)
