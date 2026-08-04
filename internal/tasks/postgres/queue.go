package postgres

import (
	"context"
	"crypto/subtle"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

type claimCandidate struct {
	Task tasks.Task
}

type leaseRecord struct {
	Lease     tasks.Lease
	TokenHash string
	Now       time.Time
}

func (s *Store) Claim(ctx context.Context, request tasks.ClaimRequest, validate tasks.AssigneeValidator, outboxMaxAttempts int) ([]tasks.ClaimedTask, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) ([]tasks.ClaimedTask, error) {
		query := `SELECT ` + taskColumns + ` FROM tasks
			WHERE organization_id=$1 AND status='ready' AND attempt_count<max_attempts AND available_at<=clock_timestamp()`
		args := []any{request.OrganizationID}
		if request.AssignedRoleID != "" {
			args = append(args, request.AssignedRoleID)
			query += fmt.Sprintf(` AND assigned_role_id=$%d`, len(args))
		}
		args = append(args, request.BatchSize)
		query += fmt.Sprintf(` ORDER BY priority DESC,available_at ASC,created_at ASC,id ASC FOR UPDATE SKIP LOCKED LIMIT $%d`, len(args))
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return nil, mapError(err)
		}
		candidates := make([]claimCandidate, 0, request.BatchSize)
		for rows.Next() {
			task, scanErr := scanTask(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			candidates = append(candidates, claimCandidate{Task: task})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, mapError(err)
		}
		rows.Close()

		claimed := make([]tasks.ClaimedTask, 0, len(candidates))
		for _, candidate := range candidates {
			check, err := validate(ctx, candidate.Task)
			if err != nil {
				return nil, err
			}
			if !check.Available {
				if _, err := transitionTask(ctx, tx, candidate.Task, tasks.StatusBlocked, "task.blocked", "assignee_unavailable", check.Reason, "system", "orgd-reconciler", nil, outboxMaxAttempts); err != nil {
					return nil, err
				}
				continue
			}
			item, err := claimOne(ctx, tx, candidate.Task, request, outboxMaxAttempts)
			if err != nil {
				return nil, err
			}
			claimed = append(claimed, item)
		}
		return claimed, nil
	})
}

func claimOne(ctx context.Context, tx pgx.Tx, task tasks.Task, request tasks.ClaimRequest, outboxMaxAttempts int) (tasks.ClaimedTask, error) {
	plainToken, tokenHash, err := newToken()
	if err != nil {
		return tasks.ClaimedTask{}, err
	}
	ordinal := task.AttemptCount + 1
	attempt, err := scanAttempt(tx.QueryRow(ctx, `
		INSERT INTO task_attempts(task_id,ordinal,state,worker_id)
		VALUES($1,$2,'leased',$3)
		RETURNING id,task_id,ordinal,state,worker_id,result_summary,failure_code,retryable,leased_at,started_at,finished_at,created_at,updated_at
	`, task.ID, ordinal, request.WorkerID))
	if err != nil {
		return tasks.ClaimedTask{}, err
	}
	lease, err := scanLease(tx.QueryRow(ctx, `
		INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,expires_at)
		VALUES($1,$2,$3,$4,'active',clock_timestamp()+make_interval(secs=>$5))
		RETURNING id,task_id,attempt_id,holder_id,status,issued_at,heartbeat_at,expires_at,released_at,release_reason
	`, task.ID, attempt.ID, tokenHash, request.WorkerID, request.LeaseDuration.Seconds()))
	if err != nil {
		return tasks.ClaimedTask{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET attempt_count=$2 WHERE id=$1`, task.ID, ordinal); err != nil {
		return tasks.ClaimedTask{}, mapError(err)
	}
	task.AttemptCount = ordinal
	updated, err := transitionTask(ctx, tx, task, tasks.StatusLeased, "task.claimed", "", "", "worker", request.WorkerID, map[string]any{"attempt_id": attempt.ID, "lease_id": lease.ID}, outboxMaxAttempts)
	if err != nil {
		return tasks.ClaimedTask{}, err
	}
	return tasks.ClaimedTask{Task: updated, Attempt: attempt, Lease: lease, LeaseToken: plainToken}, nil
}

func (s *Store) StartAttempt(ctx context.Context, command tasks.LeaseCommand, outboxMaxAttempts int) (tasks.Task, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Task, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Task{}, err
		}
		if task.Status != tasks.StatusLeased {
			return tasks.Task{}, fmt.Errorf("%w: task must be leased before start", tasks.ErrInvalidTransition)
		}
		lease, err := verifyLease(ctx, tx, command)
		if err != nil {
			return tasks.Task{}, err
		}
		if lease.Lease.HolderID != command.ActorID {
			return tasks.Task{}, tasks.ErrLeaseMismatch
		}
		result, err := tx.Exec(ctx, `
			UPDATE task_attempts SET state='running',started_at=clock_timestamp(),updated_at=clock_timestamp()
			WHERE id=$1 AND task_id=$2 AND state='leased'
		`, command.AttemptID, command.TaskID)
		if err != nil {
			return tasks.Task{}, mapError(err)
		}
		if result.RowsAffected() != 1 {
			return tasks.Task{}, fmt.Errorf("%w: attempt is not leased", tasks.ErrInvalidTransition)
		}
		return transitionTask(ctx, tx, task, tasks.StatusRunning, "task.attempt_started", "", "", "worker", command.ActorID, map[string]any{"attempt_id": command.AttemptID}, outboxMaxAttempts)
	})
}

func (s *Store) Heartbeat(ctx context.Context, command tasks.LeaseCommand, maxLeaseDuration time.Duration) (tasks.Lease, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Lease, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Lease{}, err
		}
		if task.Status != tasks.StatusLeased && task.Status != tasks.StatusRunning {
			return tasks.Lease{}, fmt.Errorf("%w: task has no heartbeat-eligible state", tasks.ErrInvalidTransition)
		}
		lease, err := verifyLease(ctx, tx, command)
		if err != nil {
			return tasks.Lease{}, err
		}
		if lease.Lease.HolderID != command.ActorID {
			return tasks.Lease{}, tasks.ErrLeaseMismatch
		}
		extension := command.Extension
		if extension > maxLeaseDuration {
			extension = maxLeaseDuration
		}
		return scanLease(tx.QueryRow(ctx, `
			UPDATE task_leases
			SET heartbeat_at=clock_timestamp(),expires_at=clock_timestamp()+make_interval(secs=>$4)
			WHERE id=$1 AND task_id=$2 AND attempt_id=$3 AND status='active'
			RETURNING id,task_id,attempt_id,holder_id,status,issued_at,heartbeat_at,expires_at,released_at,release_reason
		`, lease.Lease.ID, command.TaskID, command.AttemptID, extension.Seconds()))
	})
}

func (s *Store) RecordAttemptResult(ctx context.Context, command tasks.RecordAttemptResultCommand, policy tasks.RetryPolicy, outboxMaxAttempts int) (tasks.Task, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Task, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Task{}, err
		}
		if task.Status != tasks.StatusRunning {
			return tasks.Task{}, fmt.Errorf("%w: task must be running before recording a result", tasks.ErrInvalidTransition)
		}
		lease, err := verifyLease(ctx, tx, command.LeaseCommand)
		if err != nil {
			return tasks.Task{}, err
		}
		if lease.Lease.HolderID != command.ActorID {
			return tasks.Task{}, tasks.ErrLeaseMismatch
		}
		switch command.Result.Outcome {
		case tasks.OutcomeSucceeded:
			if err := finishAttempt(ctx, tx, command, tasks.AttemptFinished, false, "finished"); err != nil {
				return tasks.Task{}, err
			}
			return transitionTask(ctx, tx, task, tasks.StatusAwaitingVerification, "task.attempt_finished", "", "", "worker", command.ActorID, map[string]any{"attempt_id": command.AttemptID}, outboxMaxAttempts)
		case tasks.OutcomeRetryableFailure:
			if err := finishAttempt(ctx, tx, command, tasks.AttemptFailed, true, "retryable_failure"); err != nil {
				return tasks.Task{}, err
			}
			if task.AttemptCount >= task.MaxAttempts {
				if _, err := tx.Exec(ctx, `
					INSERT INTO task_dead_letters(task_id,attempt_id,reason_code,reason,attempt_count)
					VALUES($1,$2,$3,$4,$5)
				`, task.ID, command.AttemptID, command.Result.FailureCode, failureReason(command.Result), task.AttemptCount); err != nil {
					return tasks.Task{}, mapError(err)
				}
				return transitionTask(ctx, tx, task, tasks.StatusDeadLetter, "task.dead_lettered", command.Result.FailureCode, failureReason(command.Result), "worker", command.ActorID, map[string]any{"attempt_id": command.AttemptID}, outboxMaxAttempts)
			}
			delay := policy.Delay(task.AttemptCount)
			if _, err := tx.Exec(ctx, `UPDATE tasks SET available_at=clock_timestamp()+make_interval(secs=>$2) WHERE id=$1`, task.ID, delay.Seconds()); err != nil {
				return tasks.Task{}, mapError(err)
			}
			return transitionTask(ctx, tx, task, tasks.StatusRetryWait, "task.retry_scheduled", command.Result.FailureCode, failureReason(command.Result), "worker", command.ActorID, map[string]any{"attempt_id": command.AttemptID, "retry_delay_seconds": delay.Seconds()}, outboxMaxAttempts)
		case tasks.OutcomeNonRetryableFailure:
			if err := finishAttempt(ctx, tx, command, tasks.AttemptFailed, false, "non_retryable_failure"); err != nil {
				return tasks.Task{}, err
			}
			return transitionTask(ctx, tx, task, tasks.StatusFailed, "task.failed", command.Result.FailureCode, failureReason(command.Result), "worker", command.ActorID, map[string]any{"attempt_id": command.AttemptID}, outboxMaxAttempts)
		case tasks.OutcomeCancelled:
			if err := finishAttempt(ctx, tx, command, tasks.AttemptCancelled, false, "cancelled"); err != nil {
				return tasks.Task{}, err
			}
			return transitionTask(ctx, tx, task, tasks.StatusCancelled, "task.cancelled", "attempt_cancelled", failureReason(command.Result), "worker", command.ActorID, map[string]any{"attempt_id": command.AttemptID}, outboxMaxAttempts)
		default:
			return tasks.Task{}, fmt.Errorf("%w: unsupported attempt outcome", tasks.ErrInvalidInput)
		}
	})
}

func verifyLease(ctx context.Context, tx pgx.Tx, command tasks.LeaseCommand) (leaseRecord, error) {
	var record leaseRecord
	err := tx.QueryRow(ctx, `
		SELECT id,task_id,attempt_id,holder_id,status,issued_at,heartbeat_at,expires_at,released_at,release_reason,token_hash,clock_timestamp()
		FROM task_leases WHERE task_id=$1 AND attempt_id=$2 FOR UPDATE
	`, command.TaskID, command.AttemptID).Scan(
		&record.Lease.ID, &record.Lease.TaskID, &record.Lease.AttemptID, &record.Lease.HolderID,
		&record.Lease.Status, &record.Lease.IssuedAt, &record.Lease.HeartbeatAt, &record.Lease.ExpiresAt,
		&record.Lease.ReleasedAt, &record.Lease.ReleaseReason, &record.TokenHash, &record.Now,
	)
	if err != nil {
		return leaseRecord{}, mapError(err)
	}
	if record.Lease.Status != tasks.LeaseActive {
		return leaseRecord{}, tasks.ErrLeaseExpired
	}
	provided := hashToken(command.LeaseToken)
	if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(provided)) != 1 {
		return leaseRecord{}, tasks.ErrLeaseMismatch
	}
	if !record.Lease.ExpiresAt.After(record.Now) {
		return leaseRecord{}, tasks.ErrLeaseExpired
	}
	return record, nil
}

func finishAttempt(ctx context.Context, tx pgx.Tx, command tasks.RecordAttemptResultCommand, state tasks.AttemptState, retryable bool, releaseReason string) error {
	result, err := tx.Exec(ctx, `
		UPDATE task_attempts
		SET state=$3,result_summary=NULLIF($4,''),failure_code=NULLIF($5,''),retryable=$6,finished_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE id=$1 AND task_id=$2 AND state='running'
	`, command.AttemptID, command.TaskID, state, command.Result.Summary, command.Result.FailureCode, retryable)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("%w: attempt is not running", tasks.ErrInvalidTransition)
	}
	result, err = tx.Exec(ctx, `
		UPDATE task_leases SET status='released',released_at=clock_timestamp(),release_reason=$3
		WHERE task_id=$1 AND attempt_id=$2 AND status='active'
	`, command.TaskID, command.AttemptID, releaseReason)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() != 1 {
		return tasks.ErrLeaseExpired
	}
	return nil
}

func failureReason(result tasks.AttemptResult) string {
	if result.Summary != "" {
		return result.Summary
	}
	if result.FailureCode != "" {
		return result.FailureCode
	}
	return string(result.Outcome)
}
