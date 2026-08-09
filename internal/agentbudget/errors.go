package agentbudget

import "errors"

var (
	ErrInvalidRequest  = errors.New("invalid agent budget request")
	ErrBudgetNotFound  = errors.New("agent budget not found")
	ErrBudgetExceeded  = errors.New("agent budget exceeded")
	ErrParentExhausted = errors.New("parent agent budget cannot cover the child allocation")
)
