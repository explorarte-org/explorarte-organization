package runtimeadapter

import (
	"context"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// ModelCallBudget enforces the executive call budget from durable task
// attempts and model invocations. It never trusts an in-memory counter.
//
// It used to be a decorator on invocation creation: the Executive asked for an
// invocation, this counted first, and only then created one. That placement
// stopped working the moment execution moved behind the Harness, because
// invocations are now created inside Model Runtime, one layer below anything
// the Executive can wrap. Wrapping a method the productive path no longer
// calls would have removed MaxModelCalls while looking like it still enforced
// it.
//
// So the count is separated from the creation and runs as a gate immediately
// before the Harness is entered. The counting rule is unchanged, including the
// part that matters most for correctness: an attempt that already has a
// durable invocation is being resumed, is the same logical model call, and is
// not charged again.
type ModelCallBudget struct {
	Models executive.ModelInvocationReader
	Tasks  executive.TaskCoordinator
	Limits executive.Limits
}

func (b ModelCallBudget) AuthorizeModelCall(ctx context.Context, request executive.ModelCallBudgetRequest) error {
	existing, err := b.Models.FindTaskAttemptInvocations(ctx, request.TaskID, request.AttemptID)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		// Already-existing invocations for this attempt remain readable and
		// idempotently resumable even after the budget is exhausted; only a
		// NEW provider call is rejected.
		return nil
	}
	return b.validateCorrelationBudget(ctx, request)
}

func (b ModelCallBudget) validateCorrelationBudget(ctx context.Context, request executive.ModelCallBudgetRequest) error {
	tasks, err := b.Tasks.ListByCorrelation(ctx, request.CorrelationID)
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
		if task.ID == request.TaskID {
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

var _ executive.ModelBudgetGate = ModelCallBudget{}
