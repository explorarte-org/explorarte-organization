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
	// reasons are dispatch_assignment_required -- the owner/admin flow can
	// provision a fresh bounded assignment and the Task Engine can then create
	// a fresh attempt -- and model_outcome_ambiguous whose every ambiguity now
	// carries a valid host-policy resolution: unreconciledAmbiguities applies
	// that policy autonomously on its way through, so a campaign of pure-model
	// executions recovers itself here (R14) while one unresolvable execution
	// keeps the run fail-closed exactly as before. Every other reason requires
	// explicit intervention.
	if root.Status == "blocked" {
		switch root.ReasonCode {
		case "model_outcome_ambiguous":
			children, listErr := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
			if listErr != nil {
				return Run{}, listErr
			}
			open, openErr := o.unreconciledAmbiguities(ctx, root, children)
			if openErr != nil {
				return Run{}, openErr
			}
			if open {
				run, _ := o.Status(ctx, rootTaskID)
				return run, ErrModelOutcomeAmbiguous
			}
			// handled below: with no unresolved ambiguity left, reopening is
			// as safe as the dispatch_assignment_required case.
		case "indeterminate_tool_execution":
			run, _ := o.Status(ctx, rootTaskID)
			return run, ErrIndeterminateToolExecution
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

	// Adoption is decided by one thing: does THIS process hold the opaque lease
	// token for that attempt. Nothing else is evidence of ownership.
	//
	// This used to be gated on ActiveLease.HolderID == orchestratorWorkerID,
	// which read "is this lease mine" off a worker name. That was always an
	// inference rather than proof, and after the identity split it is a wrong
	// one: the holder is now a canonical execution principal, so the condition
	// stops matching and every prior-process lease would look adoptable. The
	// token is what distinguishes this process from a previous one, precisely
	// because it is deliberately never persisted, so it is now the whole rule.
	//
	// An active lease this process cannot prove it holds is a barrier, not
	// authority: the attempt keeps running somewhere or is waiting to expire,
	// and either way this process must not heartbeat it, record a result under
	// it, or start a second provider call beside it.
	for _, child := range children {
		if child.ID == root.ID || child.ActiveLease == nil {
			continue
		}
		if _, haveToken := o.localLease(child.ID); haveToken {
			continue
		}
		// Before reporting the barrier, look at what Model Runtime durably
		// knows about this attempt. A result that already exists must not be
		// lost just because the local token is gone, and an ambiguous outcome
		// must never be resolved by trying again.
		if run, handled, inspectErr := o.inspectUnadoptableAttempt(ctx, root, child); handled {
			return run, inspectErr
		}
		run, _ := o.Status(ctx, rootTaskID)
		if root.Status == "blocked" && root.ReasonCode == "dispatch_assignment_required" {
			return run, ErrDispatchAssignmentRequired
		}
		return run, ErrRunBlocked
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

// inspectUnadoptableAttempt reads what Model Runtime durably knows about an
// attempt whose lease this process cannot adopt.
//
// Only ambiguity is acted on here, and that is deliberate. An ambiguous
// invocation means nobody can say whether the provider ran, which is true
// regardless of who holds the lease and is never resolved by trying again, so
// it becomes a durable block immediately.
//
// A SUCCEEDED invocation is deliberately NOT turned into orphaned_model_result
// while the lease is still active: the process that owns that lease may be
// alive and about to record the result legitimately, and declaring the result
// orphaned now would be a verdict about another process's work. The barrier
// response (no adoption, no second provider call) is already safe. Once the
// lease expires and Reconcile moves the attempt out of leased/running,
// findOrphanedSucceededInvocation sees it and blocks with
// orphaned_model_result, which is the existing R23 behavior this preserves.
func (o *Orchestrator) inspectUnadoptableAttempt(ctx context.Context, root, child TaskRecord) (Run, bool, error) {
	if child.ActiveLease == nil {
		return Run{}, false, nil
	}
	invocations, err := o.models.FindTaskAttemptInvocations(ctx, child.ID, child.ActiveLease.AttemptID)
	if err != nil {
		return Run{}, true, err
	}
	for _, invocation := range invocations {
		if invocation.Status != "ambiguous" {
			continue
		}
		if root.Status != "blocked" || root.ReasonCode != "model_outcome_ambiguous" {
			reason := fmt.Sprintf("task=%d attempt=%d invocation=%d requires explicit inspection", child.ID, child.ActiveLease.AttemptID, invocation.ID)
			if _, blockErr := o.tasks.BlockTask(ctx, root.ID, "model_outcome_ambiguous", reason, "service", orchestratorWorkerID); blockErr != nil {
				return Run{}, true, blockErr
			}
		}
		run, _ := o.Status(ctx, root.ID)
		return run, true, ErrModelOutcomeAmbiguous
	}
	return Run{}, false, nil
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
			// An ADJUDICATED attempt is not orphaned, whatever its invocation
			// produced. Orphaned means nobody recorded what happened -- a
			// result exists and no process ever claimed it -- which is the
			// crash this guard was written for. These states all mean the
			// opposite: the orchestrator reached a decision and wrote it down.
			//
			// "failed" is the case that made the distinction matter. A model
			// can answer perfectly while its OUTPUT fails the typed contract,
			// which is recorded as model_result_contract_rejected and left
			// retryable so the task can try again. That deliberately leaves a
			// succeeded invocation behind with no adoptable lease -- exactly
			// the shape this guard looked for. AUTONOMY-SMOKE-001's root 213
			// blocked on it: the department review was on its way to a second
			// attempt and the run was declared unrecoverable instead.
			//
			// A crash between the provider answering and the decision being
			// written does NOT reach here as "failed": the attempt would still
			// be leased or running, which the same list skips for the opposite
			// reason -- nothing has been decided yet, so the barrier stands.
			if attempt.State == "finished" || attempt.State == "running" ||
				attempt.State == "leased" || attempt.State == "failed" ||
				attempt.State == "cancelled" {
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
