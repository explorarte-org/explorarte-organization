package sleep

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// evidenceReferencePrefix identifies decision-graph run evidence in
// rag_knowledge_evidence_refs. Kept in sync with candidate.go, which writes
// exactly this prefix + run ID when a run contributes to a proposed candidate.
const evidenceReferencePrefix = "decisiongraph:run:"

// rawFetchCeiling bounds how many window-matching runs are fetched from
// decision_graph_runs before the already-consolidated exclusion is applied.
// It must stay well above the largest allowed caller limit (10000, enforced
// below) so that runs already consolidated within the window never truncate
// the caller's requested page of truly-eligible experiences.
const rawFetchCeiling = 50000

type PostgresReader struct {
	pool   *pgxpool.Pool
	ledger EvidenceLedger
}

func NewPostgresReader(store *platformpostgres.Store, ledger EvidenceLedger) (*PostgresReader, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("sleep: PostgreSQL reader requires initialized store")
	}
	if ledger == nil {
		return nil, errors.New("sleep: PostgreSQL reader requires an evidence ledger")
	}
	return &PostgresReader{pool: store.Pool(), ledger: ledger}, nil
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
ORDER BY verification.created_at ASC, r.id ASC
LIMIT $4`, organizationID, from.UTC(), to.UTC(), rawFetchCeiling)
	if err != nil {
		return nil, fmt.Errorf("sleep: query eligible experiences: %w", err)
	}
	defer rows.Close()

	candidates := make([]Experience, 0)
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
		candidates = append(candidates, experience)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sleep: iterate eligible experiences: %w", err)
	}

	// The "already consolidated" exclusion is answered through rag's own
	// public API (never direct SQL against rag_knowledge_evidence_refs) so
	// this reader stays inside internal/executive's persistence boundary.
	// It is applied here, after the SQL query, rather than as a SQL-side
	// NOT EXISTS, precisely because of that boundary; consolidated is
	// applied against rawFetchCeiling candidates so the caller-requested
	// limit is filled from truly-eligible runs, not truncated early by
	// already-consolidated ones sharing the same page.
	consolidated, err := r.ledger.ExistingEvidenceReferences(ctx, organizationID, evidenceReferencePrefix)
	if err != nil {
		return nil, fmt.Errorf("sleep: check existing evidence references: %w", err)
	}
	experiences := make([]Experience, 0, limit)
	for _, experience := range candidates {
		if consolidated[evidenceReferencePrefix+strconv.FormatInt(experience.RunID, 10)] {
			continue
		}
		experiences = append(experiences, experience)
		if len(experiences) == limit {
			break
		}
	}
	return experiences, nil
}

var _ ExperienceReader = (*PostgresReader)(nil)
