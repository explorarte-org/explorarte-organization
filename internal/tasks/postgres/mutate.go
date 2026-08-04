package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) AddDependency(ctx context.Context, command tasks.AddDependencyCommand, outboxMaxAttempts int) error {
	_, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (struct{}, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return struct{}{}, err
		}
		if task.Status.Terminal() || task.Status == tasks.StatusLeased || task.Status == tasks.StatusRunning || task.Status == tasks.StatusAwaitingVerification {
			return struct{}{}, fmt.Errorf("%w: dependencies cannot be changed while task is %s", tasks.ErrInvalidTransition, task.Status)
		}
		dependency, err := lockTask(ctx, tx, command.DependsOnID)
		if err != nil {
			return struct{}{}, err
		}
		if dependency.OrganizationID != task.OrganizationID {
			return struct{}{}, fmt.Errorf("%w: dependencies must belong to the same organization", tasks.ErrConflict)
		}
		var cycle bool
		if err := tx.QueryRow(ctx, `
			WITH RECURSIVE dependency_graph(id) AS (
				SELECT depends_on_task_id FROM task_dependencies WHERE task_id=$1
				UNION
				SELECT d.depends_on_task_id
				FROM task_dependencies d JOIN dependency_graph g ON d.task_id=g.id
			)
			SELECT EXISTS(SELECT 1 FROM dependency_graph WHERE id=$2)
		`, command.DependsOnID, command.TaskID).Scan(&cycle); err != nil {
			return struct{}{}, mapError(err)
		}
		if cycle {
			return struct{}{}, tasks.ErrDependencyCycle
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO task_dependencies(task_id,depends_on_task_id) VALUES($1,$2)
			ON CONFLICT DO NOTHING
		`, command.TaskID, command.DependsOnID)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if result.RowsAffected() == 0 {
			return struct{}{}, nil
		}
		if task.Status == tasks.StatusReady && dependency.Status != tasks.StatusCompleted {
			_, err = transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", "dependency_unsatisfied", "a newly added dependency is not completed", command.ActorType, command.ActorID, map[string]any{"depends_on_task_id": command.DependsOnID}, outboxMaxAttempts)
			return struct{}{}, err
		}
		task, err = touchTask(ctx, tx, task)
		if err != nil {
			return struct{}{}, err
		}
		if err := appendTaskEvent(ctx, tx, task, nil, nil, "task.dependency_added", command.ActorType, command.ActorID, map[string]any{"depends_on_task_id": command.DependsOnID}, outboxMaxAttempts); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) AddRequirement(ctx context.Context, command tasks.AddRequirementCommand, outboxMaxAttempts int) (tasks.Requirement, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Requirement, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Requirement{}, err
		}
		switch task.Status {
		case tasks.StatusPending, tasks.StatusReady, tasks.StatusBlocked, tasks.StatusAwaitingVerification:
		default:
			return tasks.Requirement{}, fmt.Errorf("%w: requirements cannot be changed while task is %s", tasks.ErrInvalidTransition, task.Status)
		}
		requirement, err := scanRequirement(tx.QueryRow(ctx, `
			INSERT INTO task_requirements(task_id,requirement_key,requirement_type,description,required)
			VALUES($1,$2,$3,$4,$5)
			RETURNING id,task_id,requirement_key,requirement_type,description,required,status,satisfied_at,created_at,updated_at
		`, command.TaskID, command.Spec.Key, command.Spec.Type, command.Spec.Description, requiredValue(command.Spec.Required)))
		if err != nil {
			return tasks.Requirement{}, err
		}
		task, err = touchTask(ctx, tx, task)
		if err != nil {
			return tasks.Requirement{}, err
		}
		if err := appendTaskEvent(ctx, tx, task, nil, nil, "task.requirement_added", command.ActorType, command.ActorID, map[string]any{"requirement_id": requirement.ID, "requirement_type": requirement.Type}, outboxMaxAttempts); err != nil {
			return tasks.Requirement{}, err
		}
		return requirement, nil
	})
}

func (s *Store) RecordEvidence(ctx context.Context, command tasks.RecordEvidenceCommand, outboxMaxAttempts int) (tasks.Evidence, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Evidence, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Evidence{}, err
		}
		if task.Status.Terminal() {
			return tasks.Evidence{}, fmt.Errorf("%w: evidence cannot be added to terminal task %s", tasks.ErrInvalidTransition, task.Status)
		}
		var requirementStatus tasks.RequirementStatus
		if command.RequirementID != nil {
			var requirementTaskID int64
			var requirementType tasks.RequirementType
			if err := tx.QueryRow(ctx, `SELECT task_id,requirement_type,status FROM task_requirements WHERE id=$1 FOR UPDATE`, *command.RequirementID).Scan(&requirementTaskID, &requirementType, &requirementStatus); err != nil {
				return tasks.Evidence{}, mapError(err)
			}
			if requirementTaskID != command.TaskID || requirementType != command.Type {
				return tasks.Evidence{}, fmt.Errorf("%w: evidence does not match requirement", tasks.ErrConflict)
			}
			if command.Satisfies && requirementStatus != tasks.RequirementPending {
				return tasks.Evidence{}, tasks.ErrRequirementResolved
			}
		}
		metadata, err := json.Marshal(command.Metadata)
		if err != nil {
			return tasks.Evidence{}, fmt.Errorf("encode evidence metadata: %w", err)
		}
		evidence, err := scanEvidence(tx.QueryRow(ctx, `
			INSERT INTO task_evidence(task_id,requirement_id,evidence_type,reference,digest,recorded_by,metadata)
			VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)
			RETURNING id,task_id,requirement_id,evidence_type,reference,digest,recorded_by,metadata,created_at
		`, command.TaskID, command.RequirementID, command.Type, command.Reference, nullableString(command.Digest), command.RecordedBy, metadata))
		if err != nil {
			return tasks.Evidence{}, err
		}
		if command.Satisfies {
			if command.RequirementID == nil {
				return tasks.Evidence{}, fmt.Errorf("%w: satisfying evidence requires a requirement ID", tasks.ErrInvalidInput)
			}
			result, err := tx.Exec(ctx, `
				UPDATE task_requirements SET status='satisfied',satisfied_at=clock_timestamp(),updated_at=clock_timestamp()
				WHERE id=$1 AND task_id=$2 AND status='pending'
			`, *command.RequirementID, command.TaskID)
			if err != nil {
				return tasks.Evidence{}, mapError(err)
			}
			if result.RowsAffected() != 1 {
				return tasks.Evidence{}, tasks.ErrRequirementResolved
			}
		}
		task, err = touchTask(ctx, tx, task)
		if err != nil {
			return tasks.Evidence{}, err
		}
		payload := map[string]any{"evidence_id": evidence.ID, "evidence_type": evidence.Type}
		if command.RequirementID != nil {
			payload["requirement_id"] = *command.RequirementID
			payload["satisfies"] = command.Satisfies
		}
		if err := appendTaskEvent(ctx, tx, task, nil, nil, "task.evidence_recorded", "worker", command.RecordedBy, payload, outboxMaxAttempts); err != nil {
			return tasks.Evidence{}, err
		}
		return evidence, nil
	})
}

func (s *Store) Finalize(ctx context.Context, command tasks.FinalizeCommand, outboxMaxAttempts int) (tasks.Task, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Task, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Task{}, err
		}
		var target tasks.Status
		payload := map[string]any{"outcome": command.Outcome}
		switch command.Outcome {
		case tasks.FinalCompleted:
			target = tasks.StatusCompleted
			if task.Status != tasks.StatusAwaitingVerification {
				return tasks.Task{}, fmt.Errorf("%w: completed requires awaiting_verification", tasks.ErrInvalidTransition)
			}
			var unsatisfied int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM task_requirements WHERE task_id=$1 AND required AND status<>'satisfied'`, command.TaskID).Scan(&unsatisfied); err != nil {
				return tasks.Task{}, mapError(err)
			}
			if unsatisfied > 0 {
				return tasks.Task{}, fmt.Errorf("%w: %d required requirement(s) remain", tasks.ErrRequirementsUnsatisfied, unsatisfied)
			}
		case tasks.FinalNoAction:
			target = tasks.StatusNoAction
			if task.Status != tasks.StatusAwaitingVerification {
				return tasks.Task{}, fmt.Errorf("%w: no_action requires awaiting_verification", tasks.ErrInvalidTransition)
			}
		case tasks.FinalFailed:
			target = tasks.StatusFailed
			if task.Status != tasks.StatusAwaitingVerification {
				return tasks.Task{}, fmt.Errorf("%w: explicit failed finalization requires awaiting_verification; running failures must be recorded as attempt results", tasks.ErrInvalidTransition)
			}
		case tasks.FinalRejected:
			target = tasks.StatusRejected
		case tasks.FinalCancelled:
			target = tasks.StatusCancelled
			attemptID, active, leaseErr := activeLeaseAttempt(ctx, tx, task.ID)
			if leaseErr != nil {
				return tasks.Task{}, leaseErr
			}
			if active {
				if err := revokeLease(ctx, tx, task.ID, attemptID, "finalized_cancelled"); err != nil {
					return tasks.Task{}, err
				}
				payload["revoked_attempt_id"] = attemptID
			}
		default:
			return tasks.Task{}, fmt.Errorf("%w: unknown final outcome", tasks.ErrInvalidInput)
		}
		return transitionTask(ctx, tx, task, target, "task.finalized", command.ReasonCode, command.Reason, command.ActorType, command.ActorID, payload, outboxMaxAttempts)
	})
}

func (s *Store) Block(ctx context.Context, command tasks.BlockCommand, outboxMaxAttempts int) (tasks.Task, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Task, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Task{}, err
		}
		if task.Status == tasks.StatusBlocked {
			return task, nil
		}
		if task.Status.Terminal() {
			return tasks.Task{}, fmt.Errorf("%w: terminal tasks cannot be blocked", tasks.ErrInvalidTransition)
		}
		attemptID, active, err := activeLeaseAttempt(ctx, tx, task.ID)
		if err != nil {
			return tasks.Task{}, err
		}
		if active && !command.RevokeLease {
			return tasks.Task{}, tasks.ErrActiveLease
		}
		payload := map[string]any{}
		if active {
			if err := revokeLease(ctx, tx, task.ID, attemptID, "blocked"); err != nil {
				return tasks.Task{}, err
			}
			payload["revoked_attempt_id"] = attemptID
		}
		return transitionTask(ctx, tx, task, tasks.StatusBlocked, "task.blocked", command.ReasonCode, command.Reason, command.ActorType, command.ActorID, payload, outboxMaxAttempts)
	})
}

func (s *Store) Unblock(ctx context.Context, command tasks.UnblockCommand, check tasks.AssigneeCheck, outboxMaxAttempts int) (tasks.Task, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Task, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Task{}, err
		}
		if task.Status != tasks.StatusBlocked {
			return tasks.Task{}, fmt.Errorf("%w: only blocked tasks can be unblocked", tasks.ErrInvalidTransition)
		}
		if !check.Available {
			return tasks.Task{}, fmt.Errorf("%w: %s", tasks.ErrAssigneeUnavailable, check.Reason)
		}
		if task.AttemptCount >= task.MaxAttempts {
			return tasks.Task{}, fmt.Errorf("%w: maximum attempts are exhausted", tasks.ErrConflict)
		}
		dependencyState, err := inspectDependencies(ctx, tx, task.ID)
		if err != nil {
			return tasks.Task{}, err
		}
		if dependencyState.TerminalFailure {
			return tasks.Task{}, tasks.ErrDependencyUnsatisfied
		}
		target := tasks.StatusReady
		if dependencyState.Waiting || task.AvailableAt.After(dependencyState.Now) {
			target = tasks.StatusPending
		}
		return transitionTask(ctx, tx, task, target, "task.unblocked", "", "", command.ActorType, command.ActorID, map[string]any{"dependencies_waiting": dependencyState.Waiting}, outboxMaxAttempts)
	})
}

func (s *Store) Cancel(ctx context.Context, command tasks.CancelCommand, outboxMaxAttempts int) (tasks.Task, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Task, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Task{}, err
		}
		if task.Status == tasks.StatusCancelled {
			return task, nil
		}
		if task.Status.Terminal() {
			return tasks.Task{}, fmt.Errorf("%w: task is already terminal", tasks.ErrInvalidTransition)
		}
		attemptID, active, err := activeLeaseAttempt(ctx, tx, task.ID)
		if err != nil {
			return tasks.Task{}, err
		}
		payload := map[string]any{}
		if active {
			if err := revokeLease(ctx, tx, task.ID, attemptID, "cancelled"); err != nil {
				return tasks.Task{}, err
			}
			payload["revoked_attempt_id"] = attemptID
		}
		return transitionTask(ctx, tx, task, tasks.StatusCancelled, "task.cancelled", command.ReasonCode, command.Reason, command.ActorType, command.ActorID, payload, outboxMaxAttempts)
	})
}

func touchTask(ctx context.Context, tx pgx.Tx, task tasks.Task) (tasks.Task, error) {
	updated, err := scanTask(tx.QueryRow(ctx, `
		UPDATE tasks SET version=version+1,updated_at=clock_timestamp()
		WHERE id=$1 AND version=$2 RETURNING `+taskColumns,
		task.ID, task.Version,
	))
	if errors.Is(err, tasks.ErrNotFound) {
		return tasks.Task{}, fmt.Errorf("%w: task version changed", tasks.ErrConflict)
	}
	return updated, err
}

type dependencyInspection struct {
	Now             time.Time
	Waiting         bool
	TerminalFailure bool
}

func inspectDependencies(ctx context.Context, tx pgx.Tx, taskID int64) (dependencyInspection, error) {
	var result dependencyInspection
	if err := tx.QueryRow(ctx, `
		SELECT clock_timestamp(),
		       COALESCE(bool_or(t.status <> 'completed' AND t.status NOT IN ('no_action','failed','dead_letter','rejected','cancelled')),false),
		       COALESCE(bool_or(t.status IN ('no_action','failed','dead_letter','rejected','cancelled')),false)
		FROM task_dependencies d JOIN tasks t ON t.id=d.depends_on_task_id
		WHERE d.task_id=$1
	`, taskID).Scan(&result.Now, &result.Waiting, &result.TerminalFailure); err != nil {
		return dependencyInspection{}, mapError(err)
	}
	return result, nil
}

func activeLeaseAttempt(ctx context.Context, tx pgx.Tx, taskID int64) (int64, bool, error) {
	var attemptID int64
	err := tx.QueryRow(ctx, `SELECT attempt_id FROM task_leases WHERE task_id=$1 AND status='active' FOR UPDATE`, taskID).Scan(&attemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, mapError(err)
	}
	return attemptID, true, nil
}

func revokeLease(ctx context.Context, tx pgx.Tx, taskID, attemptID int64, reason string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE task_leases SET status='revoked',released_at=clock_timestamp(),release_reason=$3
		WHERE task_id=$1 AND attempt_id=$2 AND status='active'
	`, taskID, attemptID, reason); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_attempts SET state='cancelled',finished_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE id=$1 AND state IN ('leased','running')
	`, attemptID); err != nil {
		return mapError(err)
	}
	return nil
}
