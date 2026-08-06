package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func insertAudit(ctx context.Context, tx pgx.Tx, eventType, actorType, actorID, subjectType, subjectID string, payload map[string]any) error {
	body, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, eventType, actorType, actorID, subjectType, subjectID, body)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func subjectID(id int64) string { return fmt.Sprint(id) }
