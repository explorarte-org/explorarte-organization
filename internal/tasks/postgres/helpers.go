package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const taskColumns = `id,organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,task_class,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version,correlation_id,causation_id,status_reason_code,status_reason,created_at,updated_at,terminal_at`

var taskColumnsT = qualifyColumns("t", taskColumns)

func qualifyColumns(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for index := range parts {
		parts[index] = alias + "." + strings.TrimSpace(parts[index])
	}
	return strings.Join(parts, ",")
}

func scanTask(row scanner) (tasks.Task, error) {
	var value tasks.Task
	var criteria []byte
	err := row.Scan(
		&value.ID, &value.OrganizationID, &value.OrganizationRevisionID, &value.RequestedByRoleID,
		&value.AssignedRoleID, &value.AssignedUnitID, &value.TaskClass, &value.IdempotencyKey, &value.RequestHash,
		&value.Title, &value.Instructions, &criteria, &value.Status, &value.Priority, &value.AvailableAt,
		&value.MaxAttempts, &value.AttemptCount, &value.Version, &value.CorrelationID, &value.CausationID,
		&value.StatusReasonCode, &value.StatusReason, &value.CreatedAt, &value.UpdatedAt, &value.TerminalAt,
	)
	if err != nil {
		return tasks.Task{}, mapError(err)
	}
	if err := json.Unmarshal(criteria, &value.AcceptanceCriteria); err != nil {
		return tasks.Task{}, fmt.Errorf("decode task acceptance criteria: %w", err)
	}
	if value.AcceptanceCriteria == nil {
		value.AcceptanceCriteria = []string{}
	}
	return value, nil
}

func withTx[T any](ctx context.Context, pool *pgxpool.Pool, options pgx.TxOptions, fn func(pgx.Tx) (T, error)) (result T, err error) {
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = rollback(tx)
			panic(recovered)
		}
		if err != nil {
			_ = rollback(tx)
		}
	}()
	result, err = fn(tx)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return result, nil
}

func rollback(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}

func lockTask(ctx context.Context, tx pgx.Tx, taskID int64) (tasks.Task, error) {
	return scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=$1 FOR UPDATE`, taskID))
}

func transitionTask(
	ctx context.Context,
	tx pgx.Tx,
	task tasks.Task,
	to tasks.Status,
	eventType, reasonCode, reason, actorType, actorID string,
	payload map[string]any,
	outboxMaxAttempts int,
) (tasks.Task, error) {
	if err := tasks.ValidateTransition(task.Status, to); err != nil {
		return tasks.Task{}, err
	}
	from := task.Status
	terminal := to.Terminal()
	updated, err := scanTask(tx.QueryRow(ctx, `
		UPDATE tasks
		SET status=$2,
		    status_reason_code=NULLIF($3,''),
		    status_reason=NULLIF($4,''),
		    version=version+1,
		    updated_at=clock_timestamp(),
		    terminal_at=CASE WHEN $5 THEN clock_timestamp() ELSE NULL END
		WHERE id=$1 AND version=$6
		RETURNING `+taskColumns,
		task.ID, to, reasonCode, reason, terminal, task.Version,
	))
	if err != nil {
		if errors.Is(err, tasks.ErrNotFound) {
			return tasks.Task{}, fmt.Errorf("%w: task version changed", tasks.ErrConflict)
		}
		return tasks.Task{}, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if reasonCode != "" {
		payload["reason_code"] = reasonCode
	}
	if err := appendTaskEvent(ctx, tx, updated, &from, &to, eventType, actorType, actorID, payload, outboxMaxAttempts); err != nil {
		return tasks.Task{}, err
	}
	return updated, nil
}

func appendTaskEvent(
	ctx context.Context,
	tx pgx.Tx,
	task tasks.Task,
	from, to *tasks.Status,
	eventType, actorType, actorID string,
	payload map[string]any,
	outboxMaxAttempts int,
) error {
	if actorType == "" {
		actorType = "system"
	}
	if actorID == "" {
		actorID = "orgd"
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode task event payload: %w", err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM task_events WHERE task_id=$1`, task.ID).Scan(&sequence); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events(task_id,sequence,event_type,from_status,to_status,actor_type,actor_id,correlation_id,causation_id,payload)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)
	`, task.ID, sequence, eventType, statusValue(from), statusValue(to), actorType, actorID, task.CorrelationID, task.CausationID, payloadJSON); err != nil {
		return mapError(err)
	}
	outboxPayload := map[string]any{
		"schema_version": 1,
		"task_id":        task.ID,
		"event_type":     eventType,
		"task_version":   task.Version,
	}
	if from != nil {
		outboxPayload["from_status"] = *from
	}
	if to != nil {
		outboxPayload["to_status"] = *to
	}
	if reasonCode, ok := payload["reason_code"]; ok {
		outboxPayload["reason_code"] = reasonCode
	}
	outboxJSON, err := json.Marshal(outboxPayload)
	if err != nil {
		return fmt.Errorf("encode outbox payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts)
		VALUES('task',$1,$2,1,$3::jsonb,$4)
	`, strconv.FormatInt(task.ID, 10), eventType, outboxJSON, outboxMaxAttempts); err != nil {
		return mapError(err)
	}
	auditPayload := map[string]any{
		"task_id":      task.ID,
		"task_version": task.Version,
		"event_type":   eventType,
	}
	if from != nil {
		auditPayload["from_status"] = *from
	}
	if to != nil {
		auditPayload["to_status"] = *to
	}
	if reasonCode, ok := payload["reason_code"]; ok {
		auditPayload["reason_code"] = reasonCode
	}
	auditJSON, err := json.Marshal(auditPayload)
	if err != nil {
		return fmt.Errorf("encode audit payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,correlation_id,causation_id,payload)
		VALUES($1,$2,$3,'task',$4,$5,$6,$7::jsonb)
	`, eventType, actorType, actorID, strconv.FormatInt(task.ID, 10), task.CorrelationID, task.CausationID, auditJSON); err != nil {
		return mapError(err)
	}
	return nil
}

func statusValue(status *tasks.Status) any {
	if status == nil {
		return nil
	}
	return string(*status)
}

func newToken() (plain, hash string, err error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", fmt.Errorf("generate cryptographic token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buffer)
	digest := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(digest[:]), nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func requiredValue(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}
