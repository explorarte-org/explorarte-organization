package executive

import (
	"context"
	"errors"
)

// ResumeDurable is the restart-safe entry point used by the persistent worker
// and CLI. It never reconstructs or persists lease tokens. If a process died
// while an executive child held a lease, reconciliation is allowed to expire
// that lease and the next attempt must obtain its own dispatcher assignment.
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
	if root.Status == "blocked" && root.ReasonCode == "dispatch_assignment_required" {
		children, listErr := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
		if listErr != nil {
			return Run{}, listErr
		}
		for _, child := range children {
			if child.ID == root.ID || child.ActiveLease == nil {
				continue
			}
			if child.ActiveLease.HolderID == orchestratorWorkerID {
				if _, haveToken := o.localLease(child.ID); !haveToken {
					// The prior process owned the opaque token. Reusing the lease or
					// its attempt-specific assignment would violate Task Engine and R10.
					return o.Status(ctx, rootTaskID)
				}
			}
		}
		// No unrecoverable active lease remains. Re-open the root so the next
		// exact child claim creates a fresh attempt and can request/prove a fresh
		// dispatcher assignment through the administrative flow.
		if _, err = o.tasks.UnblockTask(ctx, root.ID, "service", orchestratorWorkerID); err != nil {
			return Run{}, err
		}
	}
	run, err := o.Resume(ctx, rootTaskID)
	if errors.Is(err, ErrRunBlocked) {
		return run, err
	}
	return run, err
}
