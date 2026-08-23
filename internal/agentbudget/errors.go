package agentbudget

import "errors"

var (
	ErrInvalidRequest  = errors.New("invalid agent budget request")
	ErrBudgetNotFound  = errors.New("agent budget not found")
	ErrBudgetExceeded  = errors.New("agent budget exceeded")
	ErrParentExhausted = errors.New("parent agent budget cannot cover the child allocation")
	// ErrBudgetConflict means a root budget already exists for this task and
	// describes something other than what the caller asked for.
	//
	// CreateRootBudget is idempotent, and idempotent means "asking twice for
	// the SAME thing changes nothing" -- not "asking twice succeeds". Without
	// this, a retry stating different ceilings returned the durable row and
	// reported success, so the caller believed it had set a budget the system
	// had quietly refused to change.
	ErrBudgetConflict = errors.New("agent budget already exists with a different definition")
)
