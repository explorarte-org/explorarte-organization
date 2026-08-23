package agentbudget

import (
	"context"
	"time"
)

// Ledger is the transactional boundary for agent budgets.
type Ledger interface {
	// CreateRootBudget starts a new budget tree for a root execution task.
	CreateRootBudget(ctx context.Context, organizationID string, rootTaskID int64, roleID string, limits Limits, now time.Time) (Budget, error)
	GetBudget(ctx context.Context, budgetID int64) (Budget, error)
	// ResolveBudgetForTask returns the budget any given task in the tree
	// should consume against — its own row if it was given an explicit
	// allocation, or the shared ancestor row otherwise.
	ResolveBudgetForTask(ctx context.Context, taskID int64) (Budget, error)
	// InheritForChild attaches childTaskID to a budget scope descended from
	// parentBudgetID. childDepth is the child's depth in the execution
	// tree as the caller (which already builds that tree) knows it — the
	// stored depth becomes the max observed depth so far, never a simple
	// increment, since siblings at the same level must not each push it
	// deeper. With allocation == nil, the child shares the parent's budget
	// row outright (the returned Budget.ID equals parentBudgetID); depth
	// and subagent count are still consumed against the parent. With a
	// non-nil allocation, a new budget row is created for the child with
	// its own limits, and that allocation is debited from the parent's
	// remaining budget as a permanent commitment.
	InheritForChild(ctx context.Context, parentBudgetID int64, childTaskID int64, childRoleID string, childDepth int64, allocation *Limits, now time.Time) (Budget, error)
	// ConsumeModelCall applies delta to budgetID's usage, failing closed
	// with ErrBudgetExceeded if any dimension would cross its limit.
	// invocationID is the idempotency key: a retried call for the same
	// invocation must not be consumed twice.
	ConsumeModelCall(ctx context.Context, budgetID int64, invocationID int64, delta Usage, now time.Time) error
	// SettleModelCall replaces what a call was CHARGED with what it
	// actually COST, once the provider has reported usage.
	//
	// Reserve charges before the call, and it must charge the maximum the
	// call is allowed to spend -- anything less could let a call exceed a
	// ceiling it was admitted under. That is a sound reservation and a
	// false account: AUTONOMY-SMOKE-017-R4 reserved 1,024,000 output
	// tokens across eight calls, emitted 36,753, and died of exhaustion
	// with 69% of its ceiling spent on space it never used.
	//
	// So the reservation stands only until the truth is known. actual is
	// what the provider reported; the store computes the correction
	// against the recorded charge, which is normally a refund.
	//
	// invocationID is the idempotency key, as for ConsumeModelCall: a
	// settlement applied twice must move the account once. Settling a call
	// that was never charged is a no-op, not an error, because a call can
	// fail before it is ever admitted.
	SettleModelCall(ctx context.Context, budgetID int64, invocationID int64, actual Usage, now time.Time) error
}
