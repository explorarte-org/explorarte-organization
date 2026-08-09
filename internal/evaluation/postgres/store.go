// Package postgres is durable storage (migration 000031) for R30's canary
// evaluation runs: internal/evaluation/fixtures.RunOutcome records, one row
// per (run, fixture), keyed to a run identifying which suite and subject
// (retrieval mode or organizational configuration) produced them. Rows are
// append-only — CreateRun starts a new run every call, RecordOutcome never
// updates an existing outcome row — so a comparison between two runs can
// never be confused by an outcome changing under it.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("evaluation store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("evaluation store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

// Run is one persisted evaluation_runs row, identifying the suite and
// subject a set of RunOutcomes belongs to.
type Run struct {
	ID             int64
	OrganizationID string
	SuiteID        string
	SubjectID      string
	StartedAt      time.Time
	CompletedAt    *time.Time
	CreatedBy      string
}

// CreateRun starts a new durable run. StartedAt/now are separate so a
// caller with its own clock (e.g. a fixed test clock) controls both
// independently; most callers pass the same value for both.
func (s *Store) CreateRun(ctx context.Context, suiteID, subjectID, createdBy string, startedAt time.Time) (int64, error) {
	suiteID = strings.TrimSpace(suiteID)
	subjectID = strings.TrimSpace(subjectID)
	createdBy = strings.TrimSpace(createdBy)
	if suiteID == "" || subjectID == "" || createdBy == "" {
		return 0, errors.New("evaluation store: suite id, subject id, and created_by are required")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `
INSERT INTO evaluation_runs (organization_id, suite_id, subject_id, started_at, created_by)
VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		s.organizationID, suiteID, subjectID, startedAt.UTC(), createdBy,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("evaluation store: create run: %w", err)
	}
	return id, nil
}

// CompleteRun marks a run finished. It is optional bookkeeping — a run
// with no completed_at is one that was interrupted mid-suite, which is
// itself useful evidence (an incomplete canary run must never be reported
// as if it were a full comparison).
func (s *Store) CompleteRun(ctx context.Context, runID int64, completedAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE evaluation_runs SET completed_at=$1 WHERE id=$2 AND organization_id=$3`, completedAt.UTC(), runID, s.organizationID)
	if err != nil {
		return fmt.Errorf("evaluation store: complete run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("evaluation store: run %d not found", runID)
	}
	return nil
}

// RecordOutcome persists one fixture's RunOutcome under runID. Calling it
// twice for the same (runID, FixtureID) is a caller bug, not a legitimate
// update path — the UNIQUE(run_id, fixture_id) constraint rejects it.
func (s *Store) RecordOutcome(ctx context.Context, runID int64, outcome fixtures.RunOutcome, recordedAt time.Time) error {
	if outcome.FixtureID == "" {
		return errors.New("evaluation store: outcome fixture id is required")
	}
	invariantResults, err := json.Marshal(outcome.InvariantResults)
	if err != nil {
		return fmt.Errorf("evaluation store: marshal invariant results: %w", err)
	}
	violated, err := json.Marshal(outcome.ViolatedInvariants)
	if err != nil {
		return fmt.Errorf("evaluation store: marshal violated invariants: %w", err)
	}
	evidence, err := json.Marshal(outcome.EvidenceRefs)
	if err != nil {
		return fmt.Errorf("evaluation store: marshal evidence refs: %w", err)
	}
	metrics, err := json.Marshal(outcome.Metrics)
	if err != nil {
		return fmt.Errorf("evaluation store: marshal metrics: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO evaluation_run_outcomes
    (run_id, fixture_id, passed, invariant_results, violated_invariants, evidence_refs, metrics, notes, recorded_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		runID, outcome.FixtureID, outcome.Passed, invariantResults, violated, evidence, metrics, outcome.Notes, recordedAt.UTC(),
	); err != nil {
		return fmt.Errorf("evaluation store: record outcome for fixture %s: %w", outcome.FixtureID, err)
	}
	return nil
}

func (s *Store) GetRun(ctx context.Context, runID int64) (Run, error) {
	var run Run
	if err := s.pool.QueryRow(ctx, `
SELECT id, organization_id, suite_id, subject_id, started_at, completed_at, created_by
FROM evaluation_runs WHERE id=$1 AND organization_id=$2`, runID, s.organizationID,
	).Scan(&run.ID, &run.OrganizationID, &run.SuiteID, &run.SubjectID, &run.StartedAt, &run.CompletedAt, &run.CreatedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, fmt.Errorf("evaluation store: run %d not found", runID)
		}
		return Run{}, fmt.Errorf("evaluation store: get run: %w", err)
	}
	return run, nil
}

// ListOutcomes returns every outcome recorded for runID, ordered by
// fixture id for a stable, diffable report/compare output.
func (s *Store) ListOutcomes(ctx context.Context, runID int64) ([]fixtures.RunOutcome, error) {
	rows, err := s.pool.Query(ctx, `
SELECT fixture_id, passed, invariant_results, violated_invariants, evidence_refs, metrics, notes
FROM evaluation_run_outcomes eo
JOIN evaluation_runs r ON r.id = eo.run_id
WHERE eo.run_id=$1 AND r.organization_id=$2
ORDER BY fixture_id`, runID, s.organizationID)
	if err != nil {
		return nil, fmt.Errorf("evaluation store: list outcomes: %w", err)
	}
	defer rows.Close()
	var results []fixtures.RunOutcome
	for rows.Next() {
		var outcome fixtures.RunOutcome
		var invariantResults, violated, evidence, metricsRaw []byte
		if err := rows.Scan(&outcome.FixtureID, &outcome.Passed, &invariantResults, &violated, &evidence, &metricsRaw, &outcome.Notes); err != nil {
			return nil, fmt.Errorf("evaluation store: scan outcome: %w", err)
		}
		if err := json.Unmarshal(invariantResults, &outcome.InvariantResults); err != nil {
			return nil, fmt.Errorf("evaluation store: unmarshal invariant results: %w", err)
		}
		if err := json.Unmarshal(violated, &outcome.ViolatedInvariants); err != nil {
			return nil, fmt.Errorf("evaluation store: unmarshal violated invariants: %w", err)
		}
		if err := json.Unmarshal(evidence, &outcome.EvidenceRefs); err != nil {
			return nil, fmt.Errorf("evaluation store: unmarshal evidence refs: %w", err)
		}
		if err := json.Unmarshal(metricsRaw, &outcome.Metrics); err != nil {
			return nil, fmt.Errorf("evaluation store: unmarshal metrics: %w", err)
		}
		results = append(results, outcome)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evaluation store: iterate outcomes: %w", err)
	}
	return results, nil
}
