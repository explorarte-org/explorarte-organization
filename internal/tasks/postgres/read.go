package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func (s *Store) GetTask(ctx context.Context, id int64) (tasks.TaskDetail, error) {
	task, err := scanTask(s.pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=$1`, id))
	if err != nil {
		return tasks.TaskDetail{}, err
	}
	detail := tasks.TaskDetail{Task: task}
	if detail.Dependencies, err = listDependencyTasks(ctx, s.pool, id); err != nil {
		return tasks.TaskDetail{}, err
	}
	if detail.Requirements, err = listRequirements(ctx, s.pool, id); err != nil {
		return tasks.TaskDetail{}, err
	}
	if detail.Evidence, err = listEvidence(ctx, s.pool, id); err != nil {
		return tasks.TaskDetail{}, err
	}
	if detail.Attempts, err = s.ListAttempts(ctx, id); err != nil {
		return tasks.TaskDetail{}, err
	}
	lease, leaseErr := scanLease(s.pool.QueryRow(ctx, `
		SELECT id,task_id,attempt_id,holder_id,status,issued_at,heartbeat_at,expires_at,released_at,release_reason
		FROM task_leases WHERE task_id=$1 AND status='active'
	`, id))
	if leaseErr == nil {
		detail.ActiveLease = &lease
	} else if leaseErr != tasks.ErrNotFound {
		return tasks.TaskDetail{}, leaseErr
	}
	return detail, nil
}

func (s *Store) ListTasks(ctx context.Context, filter tasks.TaskFilter) ([]tasks.Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE organization_id=$1`
	args := []any{filter.OrganizationID}
	if len(filter.Statuses) > 0 {
		statuses := make([]string, len(filter.Statuses))
		for index, status := range filter.Statuses {
			statuses[index] = string(status)
		}
		args = append(args, statuses)
		query += fmt.Sprintf(` AND status=ANY($%d::text[])`, len(args))
	}
	if filter.AssignedRoleID != "" {
		args = append(args, filter.AssignedRoleID)
		query += fmt.Sprintf(` AND assigned_role_id=$%d`, len(args))
	}
	if filter.AssignedUnitID != "" {
		args = append(args, filter.AssignedUnitID)
		query += fmt.Sprintf(` AND assigned_unit_id=$%d`, len(args))
	}
	if filter.CorrelationID != "" {
		args = append(args, filter.CorrelationID)
		query += fmt.Sprintf(` AND correlation_id=$%d`, len(args))
	}
	args = append(args, filter.Limit, filter.Offset)
	query += fmt.Sprintf(` ORDER BY created_at DESC,id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.Task, 0)
	for rows.Next() {
		value, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func (s *Store) ListEvents(ctx context.Context, taskID int64, limit int) ([]tasks.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,task_id,sequence,event_type,from_status,to_status,actor_type,actor_id,correlation_id,causation_id,payload,occurred_at
		FROM task_events WHERE task_id=$1 ORDER BY sequence LIMIT $2
	`, taskID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.Event, 0)
	for rows.Next() {
		var value tasks.Event
		var payload []byte
		if err := rows.Scan(&value.ID, &value.TaskID, &value.Sequence, &value.EventType, &value.FromStatus, &value.ToStatus, &value.ActorType, &value.ActorID, &value.CorrelationID, &value.CausationID, &payload, &value.OccurredAt); err != nil {
			return nil, mapError(err)
		}
		if err := json.Unmarshal(payload, &value.Payload); err != nil {
			return nil, fmt.Errorf("decode task event payload: %w", err)
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func (s *Store) ListAttempts(ctx context.Context, taskID int64) ([]tasks.Attempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,task_id,ordinal,state,worker_id,result_summary,failure_code,retryable,leased_at,started_at,finished_at,created_at,updated_at
		FROM task_attempts WHERE task_id=$1 ORDER BY ordinal
	`, taskID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.Attempt, 0)
	for rows.Next() {
		value, scanErr := scanAttempt(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func (s *Store) ListDeadLetters(ctx context.Context, limit int) ([]tasks.DeadLetter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,task_id,attempt_id,reason_code,reason,attempt_count,created_at,redriven_at,redrive_task_id
		FROM task_dead_letters ORDER BY created_at DESC,id DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.DeadLetter, 0)
	for rows.Next() {
		value, scanErr := scanDeadLetter(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func (s *Store) GetDeadLetter(ctx context.Context, id int64) (tasks.DeadLetter, error) {
	return scanDeadLetter(s.pool.QueryRow(ctx, `
		SELECT id,task_id,attempt_id,reason_code,reason,attempt_count,created_at,redriven_at,redrive_task_id
		FROM task_dead_letters WHERE id=$1
	`, id))
}

func listDependencyTasks(ctx context.Context, db queryer, taskID int64) ([]tasks.Task, error) {
	rows, err := db.Query(ctx, `
		SELECT `+taskColumnsT+`
		FROM tasks t JOIN task_dependencies d ON d.depends_on_task_id=t.id
		WHERE d.task_id=$1 ORDER BY t.id
	`, taskID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.Task, 0)
	for rows.Next() {
		value, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func listRequirements(ctx context.Context, db queryer, taskID int64) ([]tasks.Requirement, error) {
	rows, err := db.Query(ctx, `
		SELECT id,task_id,requirement_key,requirement_type,description,required,status,satisfied_at,created_at,updated_at
		FROM task_requirements WHERE task_id=$1 ORDER BY id
	`, taskID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.Requirement, 0)
	for rows.Next() {
		value, scanErr := scanRequirement(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func listEvidence(ctx context.Context, db queryer, taskID int64) ([]tasks.Evidence, error) {
	rows, err := db.Query(ctx, `
		SELECT id,task_id,requirement_id,evidence_type,reference,digest,recorded_by,metadata,created_at
		FROM task_evidence WHERE task_id=$1 ORDER BY id
	`, taskID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]tasks.Evidence, 0)
	for rows.Next() {
		value, scanErr := scanEvidence(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func scanRequirement(row scanner) (tasks.Requirement, error) {
	var value tasks.Requirement
	err := row.Scan(&value.ID, &value.TaskID, &value.Key, &value.Type, &value.Description, &value.Required, &value.Status, &value.SatisfiedAt, &value.CreatedAt, &value.UpdatedAt)
	return value, mapError(err)
}

func scanEvidence(row scanner) (tasks.Evidence, error) {
	var value tasks.Evidence
	var metadata []byte
	err := row.Scan(&value.ID, &value.TaskID, &value.RequirementID, &value.Type, &value.Reference, &value.Digest, &value.RecordedBy, &metadata, &value.CreatedAt)
	if err != nil {
		return tasks.Evidence{}, mapError(err)
	}
	if err := json.Unmarshal(metadata, &value.Metadata); err != nil {
		return tasks.Evidence{}, fmt.Errorf("decode task evidence metadata: %w", err)
	}
	return value, nil
}

func scanAttempt(row scanner) (tasks.Attempt, error) {
	var value tasks.Attempt
	err := row.Scan(&value.ID, &value.TaskID, &value.Ordinal, &value.State, &value.WorkerID, &value.ResultSummary, &value.FailureCode, &value.Retryable, &value.LeasedAt, &value.StartedAt, &value.FinishedAt, &value.CreatedAt, &value.UpdatedAt)
	return value, mapError(err)
}

func scanLease(row scanner) (tasks.Lease, error) {
	var value tasks.Lease
	err := row.Scan(&value.ID, &value.TaskID, &value.AttemptID, &value.HolderID, &value.Status, &value.IssuedAt, &value.HeartbeatAt, &value.ExpiresAt, &value.ReleasedAt, &value.ReleaseReason)
	return value, mapError(err)
}

func scanDeadLetter(row scanner) (tasks.DeadLetter, error) {
	var value tasks.DeadLetter
	err := row.Scan(&value.ID, &value.TaskID, &value.AttemptID, &value.ReasonCode, &value.Reason, &value.AttemptCount, &value.CreatedAt, &value.RedrivenAt, &value.RedriveTaskID)
	return value, mapError(err)
}

func statusesText(statuses []tasks.Status) string {
	values := make([]string, len(statuses))
	for i, status := range statuses {
		values[i] = string(status)
	}
	return strings.Join(values, ",")
}
