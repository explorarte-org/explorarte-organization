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
		return createInTx(ctx, tx, input)
	})
	if err != nil {
		return tasks.Task{}, false, err
	}
	return result.Task, result.Created, nil
}

// createInTx is Create's whole body, lifted out of its own transaction so
// that a caller already holding one can create a task as part of a larger
// atomic step. RedriveDeadLetter is the reason this exists: a recovery
// successor and the dead letter's redrive stamp must become durable
// together or not at all -- a successor with no stamp would be redrivable
// again, and a stamp with no successor would be a recovery that never ran.
func createInTx(ctx context.Context, tx pgx.Tx, input tasks.PreparedCreate) (createResult, error) {
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
			organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,task_class,
			idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
			max_attempts,correlation_id,causation_id,status_reason_code,status_reason
		)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,COALESCE($14::timestamptz,clock_timestamp()),$15,$16,$17,$18,$19)
		ON CONFLICT (organization_id,idempotency_key) DO NOTHING
		RETURNING `+taskColumns,
		input.Request.OrganizationID, input.OrganizationRevisionID, requester, input.Request.AssignedRoleID,
		input.AssignedUnitID, input.TaskClass, input.Request.IdempotencyKey, input.RequestHash, input.Request.Title,
		input.Request.Instructions, criteria, input.InitialStatus, input.Request.Priority, availableAt,
		input.Request.MaxAttempts, nullableString(input.Request.CorrelationID), nullableString(input.Request.CausationID),
		// Written by the INSERT itself: a coordination hold applied in a
		// second statement would leave the task claimable in between.
		nullableString(input.InitialStatusReasonCode), nullableString(input.InitialStatusReason),
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
		// M1.3 section 9: existing.RequestHash may have been computed
		// before TaskClass (or any other newly-hashed field) existed
		// at all. A direct mismatch against the fresh v2 hash is not
		// automatically a conflict -- it might be a legitimate resumed
		// pre-M1.3 request whose stored hash only matches the exact
		// pre-M1.3 (TaskClass-omitted) computation. Only when NEITHER
		// computation matches is this a genuine contradictory identity
		// under a reused idempotency key.
		if existing.RequestHash != input.RequestHash {
			legacyHash, legacyErr := tasks.HashCreateRequestLegacy(input.Request)
			if legacyErr != nil {
				return createResult{}, legacyErr
			}
			if existing.RequestHash != legacyHash {
				return createResult{}, tasks.ErrIdempotencyConflict
			}
			// The pre-M1.3 fields all agree, but HashCreateRequestLegacy
			// zeros TaskClass BEFORE hashing -- it proves nothing about
			// whether the resumed caller's TaskClass actually agrees
			// with what this row already durably records.
			//
			// input.TaskClassExplicit distinguishes "the caller omitted
			// TaskClass" (no assertion made, always compatible) from
			// "the caller explicitly asked for general.work" (a real,
			// specific classification claim like any other -- treating
			// it as automatically compatible was the exact gap
			// independent review round 2 found: a resumed caller could
			// silently contradict an already-known, specific
			// classification such as the research backfill by asking
			// for general.work).
			//
			// When the caller DID assert something and this row itself
			// never asserted a classification yet (TaskClassLegacyUnspecified,
			// the only value a historical row this migration did not
			// specifically backfill can have), that first legitimate
			// assertion is durably BOUND here -- via a CAS-style UPDATE
			// that only ever fills a still-unspecified value, never
			// overwrites an already-bound one -- so a LATER, DIFFERENT
			// resumed assertion under the same idempotency key is
			// compared against a now-concrete value instead of
			// remaining permanently unbound and permanently
			// "compatible with anything" forever (the other half of
			// round 2's finding).
			if input.TaskClassExplicit {
				if existing.TaskClass == tasks.TaskClassLegacyUnspecified {
					// The SELECT ... FOR UPDATE above already holds this
					// row's lock for the rest of this transaction, so
					// this CAS-style UPDATE (still-unspecified -> the
					// caller's asserted value) cannot lose a race: no
					// other transaction can have changed task_class
					// since we locked the row.
					bound, bindErr := scanTask(tx.QueryRow(ctx, `
						UPDATE tasks SET task_class=$1 WHERE id=$2 AND task_class=$3
						RETURNING `+taskColumns,
						input.Request.TaskClass, existing.ID, tasks.TaskClassLegacyUnspecified,
					))
					if bindErr != nil {
						return createResult{}, bindErr
					}
					existing = bound
				} else if input.Request.TaskClass != existing.TaskClass {
					return createResult{}, tasks.ErrIdempotencyConflict
				}
			}
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
}
