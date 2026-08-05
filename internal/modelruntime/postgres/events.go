package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

func appendInvocationEvent(ctx context.Context, tx pgx.Tx, inv modelruntime.Invocation, eventType, actorType, actorID string, outbox bool, outboxMax int, extra map[string]any) error {
	if _, ok := modelruntime.AllowedAuditEvents[eventType]; !ok {
		return fmt.Errorf("%w: audit event %s is not allowed", modelruntime.ErrInvalidRequest, eventType)
	}
	if outbox {
		if _, ok := modelruntime.AllowedOutboxEvents[eventType]; !ok {
			return fmt.Errorf("%w: outbox event %s is not allowed", modelruntime.ErrInvalidRequest, eventType)
		}
		if outboxMax < 1 || outboxMax > 100 {
			return fmt.Errorf("%w: invalid outbox attempt limit", modelruntime.ErrInvalidRequest)
		}
	}
	payload := map[string]any{"invocation_id": inv.ID, "task_id": inv.TaskID, "attempt_id": inv.AttemptID, "status": inv.Status, "error_code": inv.ErrorCode}
	for k, v := range extra {
		payload[k] = v
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if actorType == "" {
		actorType = "system"
	}
	if actorID == "" {
		actorID = "model-runtime"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,correlation_id,causation_id,payload) VALUES($1,$2,$3,'model_invocation',$4,NULLIF($5,''),NULLIF($6,''),$7::jsonb)`, eventType, actorType, actorID, strconv.FormatInt(inv.ID, 10), inv.CorrelationID, inv.CausationID, body); err != nil {
		return mapError(err)
	}
	if outbox {
		minimal, _ := json.Marshal(map[string]any{"schema_version": 1, "invocation_id": inv.ID, "task_id": inv.TaskID, "attempt_id": inv.AttemptID, "event_type": eventType, "status": inv.Status, "error_code": inv.ErrorCode})
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts) VALUES('model_invocation',$1,$2,1,$3::jsonb,$4)`, strconv.FormatInt(inv.ID, 10), eventType, minimal, outboxMax); err != nil {
			return mapError(err)
		}
	}
	return nil
}
