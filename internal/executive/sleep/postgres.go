package sleep

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresReader struct {
	pool *pgxpool.Pool
}

func NewPostgresReader(store *platformpostgres.Store) (*PostgresReader, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("sleep: PostgreSQL reader requires initialized store")
	}
	return &PostgresReader{pool: store.Pool()}, nil
}

func (r *PostgresReader) ListEligible(ctx context.Context, organizationID string, from, to time.Time, limit int) ([]Experience, error) {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("sleep: organization id is required")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, errors.New("sleep: invalid experience window")
	}
	if limit <= 0 || limit > 10000 {
		return nil, errors.New("sleep: experience limit outside allowed range")
	}

	// decision_verifications, not decision_records, is the authoritative
	// completion-label source. R25 intentionally creates decision_records only
	// for verified/inferred selections; contradicted/unknown outcomes remain
	// durable solely as completion verifier rows and their run stays running.
	// Filtering those rows out would erase exactly the contradictory evidence
	// consolidation must preserve.
	rows, err := r.pool.Query(ctx, `
SELECT
    r.id,
    r.task_id,
    r.attempt_id,
    t.assigned_unit_id,
    t.assigned_role_id,
    mi.provider_id,
    mi.provider_model_id,
    verification.label,
    verification.evidence_set_hash,
    COALESCE(dr.decision_hash, ''),
    verification.created_at
FROM decision_graph_runs r
JOIN tasks t
  ON t.id = r.task_id
 AND t.organization_id = r.organization_id
JOIN task_attempts ta
  ON ta.id = r.attempt_id
 AND ta.task_id = r.task_id
JOIN LATERAL (
    SELECT dv.label, dv.evidence_set_hash, dv.created_at
    FROM decision_verifications dv
    WHERE dv.run_id = r.id
      AND dv.organization_id = r.organization_id
      AND dv.verifier_ref = 'internal/completion'
      AND dv.verifier_version = 'phase2'
      AND dv.label IN ('verified','inferred','unknown','contradicted')
    ORDER BY dv.created_at DESC, dv.id DESC
    LIMIT 1
) verification ON TRUE
JOIN LATERAL (
    SELECT m.provider_id, m.provider_model_id
    FROM model_invocations m
    WHERE m.organization_id = r.organization_id
      AND m.task_id = r.task_id
      AND m.attempt_id = r.attempt_id
      AND m.status = 'succeeded'
    ORDER BY m.id DESC
    LIMIT 1
) mi ON TRUE
LEFT JOIN decision_records dr
  ON dr.run_id = r.id
 AND dr.organization_id = r.organization_id
WHERE r.organization_id = $1
  AND r.status IN ('succeeded','running')
  AND ta.state = 'finished'
  AND t.status IN ('completed','no_action','failed','blocked','dead_letter','rejected','cancelled')
  AND verification.created_at >= $2
  AND verification.created_at < $3
  AND NOT EXISTS (
      SELECT 1
      FROM rag_knowledge_evidence_refs existing
      WHERE existing.organization_id = r.organization_id
        AND existing.reference = 'decisiongraph:run:' || r.id::text
  )
ORDER BY verification.created_at ASC, r.id ASC
LIMIT $4`, organizationID, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("sleep: query eligible experiences: %w", err)
	}
	defer rows.Close()

	experiences := make([]Experience, 0)
	for rows.Next() {
		var experience Experience
		if err := rows.Scan(
			&experience.RunID, &experience.TaskID, &experience.AttemptID,
			&experience.UnitID, &experience.RoleID,
			&experience.ProviderID, &experience.ProviderModelID,
			&experience.VerificationLabel, &experience.EvidenceDigest,
			&experience.DecisionHash, &experience.ObservedAt,
		); err != nil {
			return nil, fmt.Errorf("sleep: scan eligible experience: %w", err)
		}
		experience.ObservedAt = experience.ObservedAt.UTC()
		if err := experience.Validate(); err != nil {
			return nil, fmt.Errorf("sleep: invalid durable experience run %d: %w", experience.RunID, err)
		}
		experiences = append(experiences, experience)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sleep: iterate eligible experiences: %w", err)
	}
	return experiences, nil
}

var _ ExperienceReader = (*PostgresReader)(nil)
