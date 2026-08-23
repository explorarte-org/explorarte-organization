package runtimeadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// AgentBudgets adapts internal/agentbudget.Ledger to executive.AgentBudgetProvider.
//
// It deliberately holds no Limits of its own. It used to, read from the
// process environment, and that field was the second representation of what a
// campaign may spend: whichever process reached CreateRootBudget first decided
// the campaign's ceilings for good, because the durable row is
// ON CONFLICT DO NOTHING. The ceilings now arrive from the submission that
// resolved them, so this adapter has nothing left to disagree about.
type AgentBudgets struct {
	Ledger agentbudget.Ledger
}

var _ executive.AgentBudgetProvider = AgentBudgets{}

func (a AgentBudgets) CreateRootBudget(ctx context.Context, root executive.TaskRecord, limits executive.CampaignBudget, now time.Time) error {
	_, err := a.Ledger.CreateRootBudget(ctx, root.OrganizationID, root.ID, root.AssignedRoleID, limits, now)
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
