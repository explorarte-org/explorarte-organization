package postgres

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimSpecific(ctx context.Context, taskID int64, request tasks.ClaimRequest, validate tasks.AssigneeValidator, outboxMaxAttempts int) (tasks.ClaimedTask, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.ClaimedTask, error) {
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
			return tasks.ClaimedTask{}, fmt.Errorf("%w: %s", tasks.ErrAssigneeUnavailable, check.Reason)
		}
		return claimOne(ctx, tx, task, request, outboxMaxAttempts)
	})
}

var _ tasks.SpecificClaimPersistence = (*Store)(nil)
