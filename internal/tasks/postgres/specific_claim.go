package postgres

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

// ClaimSpecific claims one already-selected task by ID -- the path
// Executive's own driveDepartments actually uses (Service.ClaimTaskByID),
// distinct from the batch Claim() the reconciler and a generic worker pool
// use.
//
// blockedErr is captured OUTSIDE the withTx callback deliberately: withTx
// rolls back the transaction on any non-nil error from fn (see withTx's own
// deferred rollback), so an error returned FROM the callback in the same
// breath as a transitionTask(...StatusBlocked...) call would silently
// discard that very transition -- the row would come back exactly as it
// was, blocked in appearance only, never durably. This was already true of
// the pre-existing assignee_unavailable branch before the fix below; both
// branches now commit their block transition first and report the failure
// to the caller only after the transaction has actually landed.
func (s *Store) ClaimSpecific(ctx context.Context, taskID int64, request tasks.ClaimRequest, validate tasks.AssigneeValidator, checkCapacity tasks.CapacityValidator, outboxMaxAttempts int) (tasks.ClaimedTask, error) {
	var blockedErr error
	claimed, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.ClaimedTask, error) {
		query := `SELECT ` + taskColumns + ` FROM tasks
WHERE id=$1 AND organization_id=$2 AND status='ready' AND attempt_count<max_attempts AND available_at<=clock_timestamp()`
		args := []any{taskID, request.OrganizationID}
		if request.AssignedRoleID != "" {
			args = append(args, request.AssignedRoleID)
			query += fmt.Sprintf(` AND assigned_role_id=$%d`, len(args))
		}
		query += ` FOR UPDATE SKIP LOCKED`
		task, err := scanTask(tx.QueryRow(ctx, query, args...))
		if err != nil {
			return tasks.ClaimedTask{}, err
		}
		check, err := validate(ctx, task)
		if err != nil {
			return tasks.ClaimedTask{}, err
		}
		if !check.Available {
			if _, err = transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", "assignee_unavailable", check.Reason, "system", "orgd-reconciler", nil, outboxMaxAttempts); err != nil {
				return tasks.ClaimedTask{}, err
			}
			blockedErr = fmt.Errorf("%w: %s", tasks.ErrAssigneeUnavailable, check.Reason)
			return tasks.ClaimedTask{}, nil
		}
		// CAPACITY_EXHAUSTION_SCHEDULING_V1 fix: this is the SAME pre-claim
		// capacity check Claim() already has -- ClaimSpecific had never
		// been given it, so a task whose pool had no eligible candidate
		// still reached claimOne through this path and consumed a real
		// attempt on a purely transient capacity gap. No attempt_count
		// consumption, no claimOne; RetryAt is persisted as available_at
		// so the existing reconcileTaskReadiness wake picks it up exactly
		// like it already does for a task blocked via the batch path.
		capCheck, err := checkCapacity(ctx, task)
		if err != nil {
			return tasks.ClaimedTask{}, err
		}
		if !capCheck.Available {
			blocked, err := transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", "capacity", capCheck.Reason, "system", "orgd-reconciler", nil, outboxMaxAttempts)
			if err != nil {
				return tasks.ClaimedTask{}, err
			}
			if !capCheck.RetryAt.IsZero() {
				if _, err := tx.Exec(ctx, `UPDATE tasks SET available_at=$2 WHERE id=$1`, blocked.ID, capCheck.RetryAt); err != nil {
					return tasks.ClaimedTask{}, mapError(err)
				}
			}
			blockedErr = fmt.Errorf("%w: %s", tasks.ErrNoCapacity, capCheck.Reason)
			return tasks.ClaimedTask{}, nil
		}
		return claimOne(ctx, tx, task, request, outboxMaxAttempts)
	})
	if err != nil {
		return tasks.ClaimedTask{}, err
	}
	if blockedErr != nil {
		return tasks.ClaimedTask{}, blockedErr
	}
	return claimed, nil
}

var _ tasks.SpecificClaimPersistence = (*Store)(nil)
