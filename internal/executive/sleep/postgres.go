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

// pageFetchSize bounds each keyset page fetched from decision_graph_runs.
// ListEligible pages through the full window on (verification.created_at,
// r.id) until it collects `limit` truly-eligible (not-yet-consolidated)
// experiences or the window is exhausted, so no single fixed-size fetch can
// truncate results out from under a window with more already-consolidated
// runs than fit in one page. A var, not a const, solely so an integration
// test can shrink it to force the multi-page path without needing
// thousands of real fixture rows; see SetPageFetchSizeForTest.
var pageFetchSize = 2000

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

	// The "already consolidated" exclusion is answered through rag's own
	// public API (never direct SQL against rag_knowledge_evidence_refs) so
	// this reader stays inside internal/executive's persistence boundary.
	consolidated, err := r.ledger.ExistingEvidenceReferences(ctx, organizationID, primaryEvidenceReferencePrefix)
	if err != nil {
		return nil, fmt.Errorf("sleep: check existing evidence references: %w", err)
	}

	experiences := make([]Experience, 0, limit)
	cursorTime := from.UTC()
	cursorID := int64(-1)
	for len(experiences) < limit {
		// decision_verifications, not decision_records, is the authoritative
		// completion-label source. R25 intentionally creates decision_records
		// only for verified/inferred selections; contradicted/unknown
		// outcomes remain durable solely as completion verifier rows. Their
		// run now closes to a real terminal status ('failed' for a fail
		// verdict, 'ambiguous' for inconclusive) instead of staying
		// 'running' forever — both must stay in this filter alongside
		// 'succeeded'/'running', or this query would start silently
		// dropping exactly the contradictory evidence consolidation must
		// preserve the moment a run's status closure catches up with it.
		//
		// The page is ordered and filtered on (verification.created_at,
		// r.id) as a keyset cursor, not a fixed-size offset/limit, so a
		// window with more already-consolidated runs than fit in one page
		// can never truncate the truly-eligible experiences that follow
		// them — the loop keeps paging until it fills the caller's limit or
		// the window itself is exhausted.
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
  AND r.status IN ('succeeded','running','failed','ambiguous')
  AND ta.state = 'finished'
  AND t.status IN ('completed','no_action','failed','blocked','dead_letter','rejected','cancelled')
  AND verification.created_at < $2
  AND (verification.created_at, r.id) > ($3, $4)
ORDER BY verification.created_at ASC, r.id ASC
LIMIT $5`, organizationID, to.UTC(), cursorTime, cursorID, pageFetchSize)
		if err != nil {
			return nil, fmt.Errorf("sleep: query eligible experiences: %w", err)
		}

		page := make([]Experience, 0, pageFetchSize)
		for rows.Next() {
			var experience Experience
			if err := rows.Scan(
				&experience.RunID, &experience.TaskID, &experience.AttemptID,
				&experience.UnitID, &experience.RoleID,
				&experience.ProviderID, &experience.ProviderModelID,
				&experience.VerificationLabel, &experience.EvidenceDigest,
				&experience.DecisionHash, &experience.ObservedAt,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sleep: scan eligible experience: %w", err)
			}
			experience.ObservedAt = experience.ObservedAt.UTC()
			if err := experience.Validate(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sleep: invalid durable experience run %d: %w", experience.RunID, err)
			}
			page = append(page, experience)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sleep: iterate eligible experiences: %w", err)
		}
		rows.Close()

		if len(page) == 0 {
			break
		}
		last := page[len(page)-1]
		cursorTime, cursorID = last.ObservedAt, last.RunID

		for _, experience := range page {
			if consolidated[primaryEvidenceReferencePrefix+strconv.FormatInt(experience.RunID, 10)] {
				continue
			}
			experiences = append(experiences, experience)
			if len(experiences) == limit {
				break
			}
		}
		if len(page) < pageFetchSize {
			break
		}
	}
	return experiences, nil
}

var _ ExperienceReader = (*PostgresReader)(nil)
