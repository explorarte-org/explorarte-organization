package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

type createResult struct {
	Task    tasks.Task
	Created bool
}

func (s *Store) Create(ctx context.Context, input tasks.PreparedCreate) (tasks.Task, bool, error) {
	result, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (createResult, error) {
		criteria, err := json.Marshal(input.Request.AcceptanceCriteria)
		if err != nil {
			return createResult{}, fmt.Errorf("encode acceptance criteria: %w", err)
		}
		requester := nullableString(input.Request.RequestedByRoleID)
		availableAt := any(nil)
		if input.Request.AvailableAt != nil {
			availableAt = *input.Request.AvailableAt
		}
		inserted, scanErr := scanTask(tx.QueryRow(ctx, `
			INSERT INTO tasks(
				organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,
				idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
				max_attempts,correlation_id,causation_id
			)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,COALESCE($13::timestamptz,clock_timestamp()),$14,$15,$16)
			ON CONFLICT (organization_id,idempotency_key) DO NOTHING
			RETURNING `+taskColumns,
			input.Request.OrganizationID, input.OrganizationRevisionID, requester, input.Request.AssignedRoleID,
			input.AssignedUnitID, input.Request.IdempotencyKey, input.RequestHash, input.Request.Title,
			input.Request.Instructions, criteria, input.InitialStatus, input.Request.Priority, availableAt,
			input.Request.MaxAttempts, nullableString(input.Request.CorrelationID), nullableString(input.Request.CausationID),
		))
		if scanErr != nil {
			if !errors.Is(scanErr, tasks.ErrNotFound) {
				return createResult{}, scanErr
			}
			existing, existingErr := scanTask(tx.QueryRow(ctx, `
				SELECT `+taskColumns+` FROM tasks WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE
			`, input.Request.OrganizationID, input.Request.IdempotencyKey))
			if existingErr != nil {
				return createResult{}, existingErr
			}
			if existing.RequestHash != input.RequestHash {
				return createResult{}, tasks.ErrIdempotencyConflict
			}
			return createResult{Task: existing, Created: false}, nil
		}
		for _, dependencyID := range input.Request.Dependencies {
			var dependencyOrganization string
			if err := tx.QueryRow(ctx, `SELECT organization_id FROM tasks WHERE id=$1`, dependencyID).Scan(&dependencyOrganization); err != nil {
				return createResult{}, mapError(err)
			}
			if dependencyOrganization != inserted.OrganizationID {
				return createResult{}, fmt.Errorf("%w: dependencies must belong to the same organization", tasks.ErrConflict)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO task_dependencies(task_id,depends_on_task_id) VALUES($1,$2)`, inserted.ID, dependencyID); err != nil {
				return createResult{}, mapError(err)
			}
		}
		for _, spec := range input.Request.Requirements {
			if _, err := tx.Exec(ctx, `
				INSERT INTO task_requirements(task_id,requirement_key,requirement_type,description,required)
				VALUES($1,$2,$3,$4,$5)
			`, inserted.ID, spec.Key, spec.Type, spec.Description, requiredValue(spec.Required)); err != nil {
				return createResult{}, mapError(err)
			}
		}
		to := inserted.Status
		if err := appendTaskEvent(ctx, tx, inserted, nil, &to, "task.created", input.ActorType, input.ActorID, map[string]any{
			"assigned_role_id":         inserted.AssignedRoleID,
			"organization_revision_id": inserted.OrganizationRevisionID,
		}, input.OutboxMaxAttempts); err != nil {
			return createResult{}, err
		}
		return createResult{Task: inserted, Created: true}, nil
	})
	if err != nil {
		return tasks.Task{}, false, err
	}
	return result.Task, result.Created, nil
}
