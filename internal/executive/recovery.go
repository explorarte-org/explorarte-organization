package executive

import (
	"context"
	"errors"
	"fmt"
)

var ErrOrphanedModelResult = errors.New("executive orphaned successful model result requires explicit reconciliation")

// ResumeDurable is the restart-safe entry point used by the persistent worker
// and CLI. Lease tokens remain process-local and are never persisted. R23 adds
// a second recovery invariant: once a prior attempt has a durable succeeded
// model invocation, a later lease expiry must never silently create another
// cognitive result for the same logical task. Such a run is blocked for
// explicit reconciliation instead of being reinferred.
func (o *Orchestrator) ResumeDurable(ctx context.Context, rootTaskID int64) (Run, error) {
	if err := o.tasks.Reconcile(ctx, 100); err != nil {
		return Run{}, err
	}
	root, err := o.tasks.GetTask(ctx, rootTaskID)
	if err != nil {
		return Run{}, err
	}
	if root.AssignedRoleID != CEORoleID || root.CorrelationID == "" {
		return Run{}, ErrInvalidInput
	}
	if root.Status == "awaiting_verification" {
		if _, err = o.gatedComplete(ctx, root); err != nil {
			return o.Status(ctx, rootTaskID)
		}
		return o.Status(ctx, rootTaskID)
	}

	// A durable blocked state is fail-closed. The only automatically reopenable
	// reason is dispatch_assignment_required, because the owner/admin flow can
	// provision a fresh bounded assignment and the Task Engine can then create a
	// fresh attempt. Every other reason requires explicit intervention.
	if root.Status == "blocked" {
		switch root.ReasonCode {
		case "model_outcome_ambiguous":
			run, _ := o.Status(ctx, rootTaskID)
			return run, ErrModelOutcomeAmbiguous
		case "orphaned_model_result":
			run, _ := o.Status(ctx, rootTaskID)
			return run, ErrOrphanedModelResult
		case "completion_verification_inconclusive":
			run, _ := o.Status(ctx, rootTaskID)
			return run, ErrCompletionInconclusive
		case "dispatch_assignment_required":
			// handled below after checking whether a prior-process lease or
			// succeeded invocation makes reopening unsafe.
		default:
			run, _ := o.Status(ctx, rootTaskID)
			return run, ErrRunBlocked
		}
	}

	children, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}

	// An active lease owned by a previous process cannot be adopted because its
	// opaque token is intentionally not durable. Wait for Task Engine
	// reconciliation; never reuse its attempt-specific assignment.
	for _, child := range children {
		if child.ID == root.ID || child.ActiveLease == nil {
			continue
		}
		if child.ActiveLease.HolderID == orchestratorWorkerID {
			if _, haveToken := o.localLease(child.ID); !haveToken {
				run, _ := o.Status(ctx, rootTaskID)
				if root.Status == "blocked" && root.ReasonCode == "dispatch_assignment_required" {
					return run, ErrDispatchAssignmentRequired
				}
				return run, ErrRunBlocked
			}
		}
	}

	if orphan, ok, detectErr := o.findOrphanedSucceededInvocation(ctx, root, children); detectErr != nil {
		return Run{}, detectErr
	} else if ok {
		reason := fmt.Sprintf("task=%d attempt=%d invocation=%d has durable succeeded output but its execution lease is no longer adoptable", orphan.TaskID, orphan.AttemptID, orphan.InvocationID)
		if root.Status != "blocked" || root.ReasonCode != "orphaned_model_result" {
			if _, err = o.tasks.BlockTask(ctx, root.ID, "orphaned_model_result", reason, "service", orchestratorWorkerID); err != nil {
				return Run{}, err
			}
		}
		run, _ := o.Status(ctx, rootTaskID)
		return run, ErrOrphanedModelResult
	}

	if root.Status == "blocked" && root.ReasonCode == "dispatch_assignment_required" {
		// With no unrecoverable active lease and no orphaned succeeded invocation,
		// it is safe to reopen the root. The next exact child claim creates a fresh
		// attempt and therefore requires a fresh assignment.
		if _, err = o.tasks.UnblockTask(ctx, root.ID, "service", orchestratorWorkerID); err != nil {
			return Run{}, err
		}
	}
	return o.Resume(ctx, rootTaskID)
}

type orphanedInvocation struct {
	TaskID       int64
	AttemptID    int64
	InvocationID int64
}

func (o *Orchestrator) findOrphanedSucceededInvocation(ctx context.Context, root TaskRecord, all []TaskRecord) (orphanedInvocation, bool, error) {
	for _, task := range all {
		if task.ID == root.ID || isTerminalTask(task.Status) || task.Status == "awaiting_verification" {
			continue
		}
		for _, attempt := range task.Attempts {
			if attempt.State == "finished" || attempt.State == "running" || attempt.State == "leased" {
				continue
			}
			invocations, err := o.models.FindTaskAttemptInvocations(ctx, task.ID, attempt.ID)
			if err != nil {
				return orphanedInvocation{}, false, err
			}
			if len(invocations) > 1 {
				return orphanedInvocation{}, false, fmt.Errorf("%w: task=%d attempt=%d has multiple invocations", ErrContractRejected, task.ID, attempt.ID)
			}
			if len(invocations) == 1 && invocations[0].Status == "succeeded" {
				if _, err = o.models.GetResult(ctx, invocations[0].ID); err != nil {
					return orphanedInvocation{}, false, err
				}
				return orphanedInvocation{TaskID: task.ID, AttemptID: attempt.ID, InvocationID: invocations[0].ID}, true, nil
			}
		}
	}
	return orphanedInvocation{}, false, nil
}
