package decisiongraph

import "errors"

var (
	ErrInvalidGraph      = errors.New("invalid decision graph")
	ErrDuplicateNode     = errors.New("duplicate decision node")
	ErrUnknownNode       = errors.New("unknown decision node")
	ErrDuplicateEdge     = errors.New("duplicate decision edge")
	ErrDependencyCycle   = errors.New("decision dependency cycle")
	ErrInvalidTransition = errors.New("invalid decision state transition")
	ErrInvalidBudget     = errors.New("invalid decision budget")
	ErrBudgetExceeded    = errors.New("decision budget exceeded")
	ErrBudgetOverflow    = errors.New("decision budget overflow")
	ErrInvalidDecision   = errors.New("invalid decision record")
)
