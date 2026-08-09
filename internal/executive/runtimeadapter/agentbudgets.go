package runtimeadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// AgentBudgets adapts internal/agentbudget.Ledger to executive.AgentBudgetProvider.
type AgentBudgets struct {
	Ledger agentbudget.Ledger
	Limits agentbudget.Limits
}

var _ executive.AgentBudgetProvider = AgentBudgets{}

func (a AgentBudgets) CreateRootBudget(ctx context.Context, root executive.TaskRecord, now time.Time) error {
	_, err := a.Ledger.CreateRootBudget(ctx, root.OrganizationID, root.ID, root.AssignedRoleID, a.Limits, now)
	return err
}

func (a AgentBudgets) InheritForChild(ctx context.Context, root, child executive.TaskRecord, depth int64, now time.Time) error {
	parent, err := a.Ledger.ResolveBudgetForTask(ctx, root.ID)
	if err != nil {
		return fmt.Errorf("resolve root budget for task %d: %w", root.ID, err)
	}
	_, err = a.Ledger.InheritForChild(ctx, parent.ID, child.ID, child.AssignedRoleID, depth, nil, now)
	return err
}
