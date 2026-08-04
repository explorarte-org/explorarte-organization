package tasks

import "errors"

var (
	ErrNotFound                = errors.New("task engine entity not found")
	ErrConflict                = errors.New("task engine conflict")
	ErrInvalidInput            = errors.New("task engine invalid input")
	ErrForbiddenField          = errors.New("task request contains forbidden field")
	ErrInvalidTransition       = errors.New("task state transition is not allowed")
	ErrIdempotencyConflict     = errors.New("task idempotency key conflicts with a different request")
	ErrDependencyCycle         = errors.New("task dependency would create a cycle")
	ErrDependencyUnsatisfied   = errors.New("task dependencies are not satisfied")
	ErrRequirementsUnsatisfied = errors.New("task requirements are not satisfied")
	ErrAssigneeUnavailable     = errors.New("task assignee is unavailable")
	ErrLeaseMismatch           = errors.New("task lease token does not match")
	ErrLeaseExpired            = errors.New("task lease has expired")
	ErrRequirementResolved     = errors.New("task requirement is already resolved")
	ErrActiveLease             = errors.New("task has an active lease")
	ErrDatabaseUnavailable     = errors.New("task engine database unavailable")
)
