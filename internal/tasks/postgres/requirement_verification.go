package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RecordRequirementVerification(ctx context.Context, command tasks.RequirementVerificationCommand, outboxMaxAttempts int) (tasks.Evidence, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.Evidence, error) {
		task, err := lockTask(ctx, tx, command.TaskID)
		if err != nil {
			return tasks.Evidence{}, err
		}
		if task.Status.Terminal() {
			return tasks.Evidence{}, fmt.Errorf("%w: cannot verify a terminal task", tasks.ErrInvalidTransition)
		}
		var requirement tasks.Requirement
		if err := tx.QueryRow(ctx, `
			SELECT id,task_id,requirement_key,requirement_type,description,required,status,satisfied_at,created_at,updated_at
			FROM task_requirements WHERE id=$1 AND task_id=$2 FOR UPDATE
		`, command.RequirementID, command.TaskID).Scan(
			&requirement.ID, &requirement.TaskID, &requirement.Key, &requirement.Type, &requirement.Description,
			&requirement.Required, &requirement.Status, &requirement.SatisfiedAt, &requirement.CreatedAt, &requirement.UpdatedAt,
		); err != nil {
			return tasks.Evidence{}, mapError(err)
		}
		if requirement.Status != tasks.RequirementPending {
			return tasks.Evidence{}, tasks.ErrRequirementResolved
		}
		metadata, err := json.Marshal(command.Metadata)
		if err != nil {
			return tasks.Evidence{}, fmt.Errorf("encode verification metadata: %w", err)
		}
		evidence, err := scanEvidence(tx.QueryRow(ctx, `
			INSERT INTO task_evidence(task_id,requirement_id,evidence_type,reference,digest,recorded_by,metadata)
			VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)
			RETURNING id,task_id,requirement_id,evidence_type,reference,digest,recorded_by,metadata,created_at
		`, command.TaskID, command.RequirementID, requirement.Type, command.Reference, nullableString(command.Digest), command.RecordedBy, metadata))
		if err != nil {
			return tasks.Evidence{}, err
		}
		status := tasks.RequirementSatisfied
		eventType := "task.requirement_satisfied"
		if command.Result == tasks.RequirementVerificationFailed {
			status = tasks.RequirementFailed
			eventType = "task.requirement_failed"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_requirements
			SET status=$2,
			    satisfied_at=CASE WHEN $2='satisfied' THEN clock_timestamp() ELSE NULL END,
			    updated_at=clock_timestamp()
			WHERE id=$1 AND status='pending'
		`, command.RequirementID, status); err != nil {
			return tasks.Evidence{}, mapError(err)
		}
		task, err = touchTask(ctx, tx, task)
		if err != nil {
			return tasks.Evidence{}, err
		}
		payload := map[string]any{
			"requirement_id":      command.RequirementID,
			"requirement_type":    requirement.Type,
			"verification_result": command.Result,
			"evidence_id":         evidence.ID,
		}
		if err := appendTaskEvent(ctx, tx, task, nil, nil, eventType, "role", command.RecordedBy, payload, outboxMaxAttempts); err != nil {
			return tasks.Evidence{}, err
		}
		return evidence, nil
	})
}
