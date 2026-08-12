package tasks

import "errors"

var (
	ErrNotFound     = errors.New("task engine entity not found")
	ErrConflict     = errors.New("task engine conflict")
	ErrInvalidInput = errors.New("task engine invalid input")
	// ErrSecretRejected marks free text that carries credential material.
	// Secrets are refused at ingress rather than redacted: rewriting an
	// instruction on the way in changes its meaning, and a task that reports
	// success on quietly mutilated instructions is a worse outcome than one
	// that refuses to start. Sensitive-but-legitimate data (personal,
	// clinical, commercially confidential) is NOT covered here -- that is
	// governed by classification and access control.
	ErrSecretRejected          = errors.New("task engine rejected credential material in free text")
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
