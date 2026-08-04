package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Reconcile(ctx context.Context, batch int, validate tasks.AssigneeValidator, policy tasks.RetryPolicy, outboxMaxAttempts int) (tasks.ReconcileResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.ReconcileResult, error) {
		var result tasks.ReconcileResult
		expired, err := reconcileExpiredLeases(ctx, tx, batch, policy, outboxMaxAttempts)
		if err != nil {
			return tasks.ReconcileResult{}, err
		}
		result.ExpiredLeases = expired
		recovered, err := recoverOutboxTx(ctx, tx, batch)
		if err != nil {
			return tasks.ReconcileResult{}, err
		}
		result.RecoveredOutbox = recovered
		promoted, blockedDependencies, blockedAssignees, err := reconcileTaskReadiness(ctx, tx, batch, validate, outboxMaxAttempts)
		if err != nil {
			return tasks.ReconcileResult{}, err
		}
		result.PromotedTasks = promoted
		result.BlockedDependencies = blockedDependencies
		result.BlockedAssignees = blockedAssignees
		return result, nil
	})
}

func reconcileExpiredLeases(ctx context.Context, tx pgx.Tx, batch int, policy tasks.RetryPolicy, outboxMaxAttempts int) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,task_id,attempt_id FROM task_leases
		WHERE status='active' AND expires_at<=clock_timestamp()
		ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $1
	`, batch)
	if err != nil {
		return 0, mapError(err)
	}
	type expiredLease struct{ leaseID, taskID, attemptID int64 }
	items := make([]expiredLease, 0, batch)
	for rows.Next() {
		var item expiredLease
		if err := rows.Scan(&item.leaseID, &item.taskID, &item.attemptID); err != nil {
			rows.Close()
			return 0, mapError(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, mapError(err)
	}
	rows.Close()
	processed := 0
	for _, item := range items {
		task, err := lockTask(ctx, tx, item.taskID)
		if err != nil {
			return processed, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_leases SET status='expired',released_at=clock_timestamp(),release_reason='lease_expired'
			WHERE id=$1 AND status='active'
		`, item.leaseID); err != nil {
			return processed, mapError(err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_attempts SET state='lease_expired',retryable=true,failure_code='lease_expired',
			    finished_at=clock_timestamp(),updated_at=clock_timestamp()
			WHERE id=$1 AND state IN ('leased','running')
		`, item.attemptID); err != nil {
			return processed, mapError(err)
		}
		if task.Status != tasks.StatusLeased && task.Status != tasks.StatusRunning {
			processed++
			continue
		}
		if task.AttemptCount >= task.MaxAttempts {
			if _, err := tx.Exec(ctx, `
				INSERT INTO task_dead_letters(task_id,attempt_id,reason_code,reason,attempt_count)
				VALUES($1,$2,'lease_expired','lease expired and maximum attempts were exhausted',$3)
				ON CONFLICT (task_id) DO NOTHING
			`, task.ID, item.attemptID, task.AttemptCount); err != nil {
				return processed, mapError(err)
			}
			if _, err := transitionTask(ctx, tx, task, tasks.StatusDeadLetter, "task.dead_lettered", "lease_expired", "lease expired and maximum attempts were exhausted", "system", "orgd-reconciler", map[string]any{"attempt_id": item.attemptID}, outboxMaxAttempts); err != nil {
				return processed, err
			}
		} else {
			delay := policy.Delay(task.AttemptCount)
			if _, err := tx.Exec(ctx, `UPDATE tasks SET available_at=clock_timestamp()+make_interval(secs=>$2) WHERE id=$1`, task.ID, delay.Seconds()); err != nil {
				return processed, mapError(err)
			}
			if _, err := transitionTask(ctx, tx, task, tasks.StatusRetryWait, "task.lease_expired", "lease_expired", "active task lease expired", "system", "orgd-reconciler", map[string]any{"attempt_id": item.attemptID, "retry_delay_seconds": delay.Seconds()}, outboxMaxAttempts); err != nil {
				return processed, err
			}
		}
		processed++
	}
	return processed, nil
}

func reconcileTaskReadiness(ctx context.Context, tx pgx.Tx, batch int, validate tasks.AssigneeValidator, outboxMaxAttempts int) (int, int, int, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE available_at<=clock_timestamp()
		  AND (
		    status IN ('pending','retry_wait')
		    OR (status='blocked' AND status_reason_code IN ('dependency_unsatisfied','dependency_terminal','assignee_unavailable'))
		  )
		ORDER BY available_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT $1
	`, batch)
	if err != nil {
		return 0, 0, 0, mapError(err)
	}
	candidates := make([]tasks.Task, 0, batch)
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			rows.Close()
			return 0, 0, 0, scanErr
		}
		candidates = append(candidates, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, 0, mapError(err)
	}
	rows.Close()

	promoted, blockedDependencies, blockedAssignees := 0, 0, 0
	for _, task := range candidates {
		dependencies, err := inspectDependencies(ctx, tx, task.ID)
		if err != nil {
			return promoted, blockedDependencies, blockedAssignees, err
		}
		if dependencies.TerminalFailure {
			changed := task.Status != tasks.StatusBlocked || task.StatusReasonCode == nil || *task.StatusReasonCode != "dependency_terminal"
			if changed {
				if task.Status == tasks.StatusBlocked {
					updated, err := updateBlockedReason(ctx, tx, task, "dependency_terminal", "a dependency ended without completed status")
					if err != nil {
						return promoted, blockedDependencies, blockedAssignees, err
					}
					if err := appendTaskEvent(ctx, tx, updated, nil, nil, "task.dependency_blocked", "system", "orgd-reconciler", map[string]any{"reason_code": "dependency_terminal"}, outboxMaxAttempts); err != nil {
						return promoted, blockedDependencies, blockedAssignees, err
					}
				} else if _, err := transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", "dependency_terminal", "a dependency ended without completed status", "system", "orgd-reconciler", nil, outboxMaxAttempts); err != nil {
					return promoted, blockedDependencies, blockedAssignees, err
				}
				blockedDependencies++
			}
			continue
		}
		if dependencies.Waiting {
			if task.Status == tasks.StatusPending || task.Status == tasks.StatusBlocked {
				continue
			}
			if _, err := transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", "dependency_unsatisfied", "one or more dependencies are not completed", "system", "orgd-reconciler", nil, outboxMaxAttempts); err != nil {
				return promoted, blockedDependencies, blockedAssignees, err
			}
			blockedDependencies++
			continue
		}
		check, err := validate(ctx, task)
		if err != nil {
			return promoted, blockedDependencies, blockedAssignees, err
		}
		if !check.Available {
			changed := task.Status != tasks.StatusBlocked || task.StatusReasonCode == nil || *task.StatusReasonCode != "assignee_unavailable"
			if changed {
				if task.Status == tasks.StatusBlocked {
					updated, err := updateBlockedReason(ctx, tx, task, "assignee_unavailable", check.Reason)
					if err != nil {
						return promoted, blockedDependencies, blockedAssignees, err
					}
					if err := appendTaskEvent(ctx, tx, updated, nil, nil, "task.assignee_blocked", "system", "orgd-reconciler", map[string]any{"reason_code": "assignee_unavailable"}, outboxMaxAttempts); err != nil {
						return promoted, blockedDependencies, blockedAssignees, err
					}
				} else if _, err := transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", "assignee_unavailable", check.Reason, "system", "orgd-reconciler", nil, outboxMaxAttempts); err != nil {
					return promoted, blockedDependencies, blockedAssignees, err
				}
				blockedAssignees++
			}
			continue
		}
		if task.AttemptCount >= task.MaxAttempts {
			// Explicit revocation may leave a blocked task with no remaining attempt.
			// Do not silently make it claimable again; an owner must cancel or inspect it.
			continue
		}
		if _, err := transitionTask(ctx, tx, task, tasks.StatusReady, "task.ready", "", "", "system", "orgd-reconciler", nil, outboxMaxAttempts); err != nil {
			return promoted, blockedDependencies, blockedAssignees, fmt.Errorf("promote task %d: %w", task.ID, err)
		}
		promoted++
	}
	return promoted, blockedDependencies, blockedAssignees, nil
}

func updateBlockedReason(ctx context.Context, tx pgx.Tx, task tasks.Task, reasonCode, reason string) (tasks.Task, error) {
	updated, err := scanTask(tx.QueryRow(ctx, `
		UPDATE tasks
		SET status_reason_code=$3,status_reason=$4,version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 AND status='blocked'
		RETURNING `+taskColumns,
		task.ID, task.Version, reasonCode, reason,
	))
	if err != nil {
		if errors.Is(err, tasks.ErrNotFound) {
			return tasks.Task{}, fmt.Errorf("%w: task version changed", tasks.ErrConflict)
		}
		return tasks.Task{}, err
	}
	return updated, nil
}
