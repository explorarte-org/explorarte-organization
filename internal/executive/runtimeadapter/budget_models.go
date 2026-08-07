package runtimeadapter

import (
	"context"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// BudgetModels enforces the executive call budget from durable task attempts
// and model invocations. It never trusts an in-memory counter. Existing
// invocations for the same task attempt remain readable/idempotently reusable
// even after the budget is exhausted; only creation of a new invocation is
// rejected.
type BudgetModels struct {
	Models executive.ModelCoordinator
	Tasks  executive.TaskCoordinator
	Limits executive.Limits
}

func (b BudgetModels) EnsureInvocation(ctx context.Context, command executive.InvocationCommand) (executive.InvocationRecord, bool, error) {
	existing, err := b.Models.FindTaskAttemptInvocations(ctx, command.TaskID, command.AttemptID)
	if err != nil {
		return executive.InvocationRecord{}, false, err
	}
	if len(existing) > 0 {
		return b.Models.EnsureInvocation(ctx, command)
	}
	if err = b.validateCorrelationBudget(ctx, command); err != nil {
		return executive.InvocationRecord{}, false, err
	}
	return b.Models.EnsureInvocation(ctx, command)
}

func (b BudgetModels) GetInvocation(ctx context.Context, id int64) (executive.InvocationRecord, error) {
	return b.Models.GetInvocation(ctx, id)
}

func (b BudgetModels) FindTaskAttemptInvocations(ctx context.Context, taskID, attemptID int64) ([]executive.InvocationRecord, error) {
	return b.Models.FindTaskAttemptInvocations(ctx, taskID, attemptID)
}

func (b BudgetModels) GetResult(ctx context.Context, id int64) (executive.InvocationResult, error) {
	return b.Models.GetResult(ctx, id)
}

func (b BudgetModels) validateCorrelationBudget(ctx context.Context, command executive.InvocationCommand) error {
	tasks, err := b.Tasks.ListByCorrelation(ctx, command.CorrelationID)
	if err != nil {
		return err
	}
	limits := b.Limits
	if limits.MaxModelCalls <= 0 {
		limits = executive.DefaultLimits()
	}
	budget := executive.InvocationBudget{}
	departments := map[string]struct{}{}
	var target *executive.TaskRecord
	for i := range tasks {
		task := tasks[i]
		if task.ID == command.TaskID {
			target = &tasks[i]
		}
		if strings.Contains(task.IdempotencyKey, ":leader-plan:") {
			dept := suffixAfter(task.IdempotencyKey, ":leader-plan:")
			if idx := strings.IndexByte(dept, ':'); idx >= 0 {
				dept = dept[:idx]
			}
			if dept != "" {
				departments[dept] = struct{}{}
			}
		}
		if strings.Contains(task.IdempotencyKey, ":leader-review:") && strings.Contains(task.IdempotencyKey, ":replan:") {
			budget.Replans++
		}
		for _, attempt := range task.Attempts {
			invocations, findErr := b.Models.FindTaskAttemptInvocations(ctx, task.ID, attempt.ID)
			if findErr != nil {
				return findErr
			}
			for range invocations {
				incrementInvocationBudget(&budget, task)
			}
		}
	}
	if target == nil {
		return executive.ErrContractRejected
	}
	// Validate the state *after* the requested new invocation would exist.
	incrementInvocationBudget(&budget, *target)
	return budget.Validate(limits, len(departments))
}

func incrementInvocationBudget(budget *executive.InvocationBudget, task executive.TaskRecord) {
	switch {
	case task.AssignedRoleID == executive.CEORoleID:
		budget.CEOCalls++
	case strings.Contains(task.IdempotencyKey, ":leader-plan:"), strings.Contains(task.IdempotencyKey, ":leader-review:"):
		budget.LeaderCalls++
	case strings.Contains(task.IdempotencyKey, ":worker:"):
		budget.WorkerAttempts++
	default:
		budget.WorkerAttempts++
	}
}

func suffixAfter(value, marker string) string {
	idx := strings.Index(value, marker)
	if idx < 0 {
		return ""
	}
	return value[idx+len(marker):]
}

var _ executive.ModelCoordinator = BudgetModels{}
