// Package postgres implements internal/shadowverifier's ports by querying
// Rama 03's registry tables and Rama 06's authorization_requests directly
// via SQL — never importing internal/organization/registry or
// internal/authorization, the same read-another-branch's-tables pattern
// internal/completion and internal/decisiongraphtrace established. Unlike
// those packages, shadowverifier owns durable state of its own: runs and
// findings persist to shadow_verifier_runs / shadow_verifier_divergences
// (migration 000014), and writes never touch any other table.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/shadowverifier"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("shadowverifier store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("shadowverifier store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var (
	_ shadowverifier.FactReader    = (*Store)(nil)
	_ shadowverifier.TrafficReader = (*Store)(nil)
	_ shadowverifier.FindingWriter = (*Store)(nil)
)

// LoadSnapshot reads the shadow's whole-organization projection in five
// bounded queries: the organization row, its non-retired units and roles, the
// current revision's reporting lines, and the revision's recorded
// capability-matrix semantic hash. Everything after this is in-memory.
func (s *Store) LoadSnapshot(ctx context.Context) (shadowverifier.Snapshot, error) {
	var snap shadowverifier.Snapshot
	var retired bool
	err := s.pool.QueryRow(ctx, `
SELECT id, owner_role_id, ceo_role_id, COALESCE(current_revision_id,0), retired_at IS NOT NULL
FROM organizations WHERE id=$1`, s.organizationID).Scan(
		&snap.Organization.ID, &snap.Organization.OwnerRoleID, &snap.Organization.CEORoleID, &snap.Organization.CurrentRevision, &retired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return shadowverifier.Snapshot{}, fmt.Errorf("%w: organization %q not found", shadowverifier.ErrSnapshotUnavailable, s.organizationID)
	}
	if err != nil {
		return shadowverifier.Snapshot{}, err
	}
	snap.Organization.Retired = retired
	snap.RevisionID = snap.Organization.CurrentRevision
	if snap.RevisionID <= 0 {
		return snap, nil
	}

	rows, err := s.pool.Query(ctx, `
SELECT id, kind, operational, leaderless, COALESCE(leader_role_id,''), retired_at IS NOT NULL
FROM organizational_units WHERE organization_id=$1 AND retired_at IS NULL ORDER BY id`, s.organizationID)
	if err != nil {
		return shadowverifier.Snapshot{}, err
	}
	for rows.Next() {
		var unit shadowverifier.UnitFact
		if err := rows.Scan(&unit.ID, &unit.Kind, &unit.Operational, &unit.Leaderless, &unit.LeaderRoleID, &unit.Retired); err != nil {
			rows.Close()
			return shadowverifier.Snapshot{}, err
		}
		snap.Units = append(snap.Units, unit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return shadowverifier.Snapshot{}, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
SELECT id, unit_id, authority_class, runtime_kind, canonical_leader, enabled, executable
FROM organization_roles WHERE organization_id=$1 AND retired_at IS NULL ORDER BY id`, s.organizationID)
	if err != nil {
		return shadowverifier.Snapshot{}, err
	}
	for rows.Next() {
		var role shadowverifier.RoleFact
		if err := rows.Scan(&role.ID, &role.UnitID, &role.AuthorityClass, &role.RuntimeKind, &role.CanonicalLeader, &role.Enabled, &role.Executable); err != nil {
			rows.Close()
			return shadowverifier.Snapshot{}, err
		}
		snap.Roles = append(snap.Roles, role)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return shadowverifier.Snapshot{}, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
SELECT role_id, reports_to_role_id, relationship
FROM organization_reporting_lines WHERE organization_id=$1 AND revision_id=$2
ORDER BY role_id, reports_to_role_id, relationship`, s.organizationID, snap.RevisionID)
	if err != nil {
		return shadowverifier.Snapshot{}, err
	}
	for rows.Next() {
		var line shadowverifier.ReportingLineFact
		if err := rows.Scan(&line.RoleID, &line.ReportsToRoleID, &line.Relationship); err != nil {
			rows.Close()
			return shadowverifier.Snapshot{}, err
		}
		snap.ReportingLines = append(snap.ReportingLines, line)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return shadowverifier.Snapshot{}, err
	}
	rows.Close()

	err = s.pool.QueryRow(ctx, `
SELECT semantic_hash FROM organization_registry_revision_documents
WHERE revision_id=$1 AND document_path='capability-matrix.yaml'`, snap.RevisionID).Scan(&snap.MatrixHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return shadowverifier.Snapshot{}, err
	}
	return snap, nil
}

// RecordedRequests returns the most recent authorization_requests rows —
// Rama 06's own record of real approval traffic, read directly.
func (s *Store) RecordedRequests(ctx context.Context, limit int) ([]shadowverifier.RecordedRequest, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, requester_role_id, capability_id, organization_revision_id, capability_matrix_hash, status
FROM authorization_requests WHERE organization_id=$1 ORDER BY id DESC LIMIT $2`, s.organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []shadowverifier.RecordedRequest
	for rows.Next() {
		var value shadowverifier.RecordedRequest
		if err := rows.Scan(&value.ID, &value.RequesterRoleID, &value.CapabilityID, &value.OrganizationRevisionID, &value.CapabilityMatrixHash, &value.Status); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// StartRun inserts the run header and returns its ID.
func (s *Store) StartRun(ctx context.Context, run shadowverifier.RunRecord) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO shadow_verifier_runs(organization_id, mode, organization_revision_id, capability_matrix_hash, started_at, status)
VALUES($1,$2,$3,$4,$5,'running') RETURNING id`,
		s.organizationID, string(run.Mode), run.RevisionID, run.MatrixHash, run.StartedAt).Scan(&id)
	return id, err
}

// FinishRun closes the run with its tally.
func (s *Store) FinishRun(ctx context.Context, runID int64, summary shadowverifier.RunSummary, status string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE shadow_verifier_runs
SET finished_at=NOW(), status=$2, checks_total=$3, checks_parity=$4, checks_divergent=$5, checks_counterexample=$6, checks_uncomparable=$7
WHERE id=$1 AND organization_id=$8`,
		runID, status, summary.ChecksTotal, summary.ChecksParity, summary.ChecksDivergent, summary.ChecksCounterexample, summary.ChecksUncomparable, s.organizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return shadowverifier.ErrRunNotFound
	}
	return nil
}

// RecordFindings persists one run's divergences and counterexamples.
func (s *Store) RecordFindings(ctx context.Context, runID int64, findings []shadowverifier.Finding) error {
	for _, finding := range findings {
		_, err := s.pool.Exec(ctx, `
INSERT INTO shadow_verifier_divergences(run_id, organization_id, fact, kind, subject_role_id, subject_unit_id, capability_id, target_role_id, shadow_verdict, ground_verdict, detail, detected_at)
VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,NULLIF($10,''),$11,$12)`,
			runID, s.organizationID, string(finding.Fact), string(finding.Kind),
			finding.SubjectRoleID, finding.SubjectUnitID, finding.CapabilityID, finding.TargetRoleID,
			finding.ShadowVerdict, finding.GroundVerdict, finding.Detail, finding.DetectedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetRun reads one run header plus its tally.
func (s *Store) GetRun(ctx context.Context, runID int64) (shadowverifier.RunRecord, shadowverifier.RunSummary, error) {
	var record shadowverifier.RunRecord
	var summary shadowverifier.RunSummary
	var mode string
	err := s.pool.QueryRow(ctx, `
SELECT id, organization_id, mode, COALESCE(organization_revision_id,0), capability_matrix_hash, started_at,
       COALESCE(finished_at,'infinity'::timestamptz), status,
       checks_total, checks_parity, checks_divergent, checks_counterexample, checks_uncomparable
FROM shadow_verifier_runs WHERE id=$1 AND organization_id=$2`, runID, s.organizationID).Scan(
		&record.ID, &record.OrganizationID, &mode, &record.RevisionID, &record.MatrixHash, &record.StartedAt,
		&record.FinishedAt, &record.Status,
		&summary.ChecksTotal, &summary.ChecksParity, &summary.ChecksDivergent, &summary.ChecksCounterexample, &summary.ChecksUncomparable,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return shadowverifier.RunRecord{}, shadowverifier.RunSummary{}, shadowverifier.ErrRunNotFound
	}
	if err != nil {
		return shadowverifier.RunRecord{}, shadowverifier.RunSummary{}, err
	}
	record.Mode = shadowverifier.RunMode(mode)
	return record, summary, nil
}

// ListRuns returns the most recent runs.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]shadowverifier.RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, organization_id, mode, COALESCE(organization_revision_id,0), capability_matrix_hash, started_at,
       COALESCE(finished_at,'infinity'::timestamptz), status
FROM shadow_verifier_runs WHERE organization_id=$1 ORDER BY id DESC LIMIT $2`, s.organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []shadowverifier.RunRecord
	for rows.Next() {
		var value shadowverifier.RunRecord
		var mode string
		if err := rows.Scan(&value.ID, &value.OrganizationID, &mode, &value.RevisionID, &value.MatrixHash, &value.StartedAt, &value.FinishedAt, &value.Status); err != nil {
			return nil, err
		}
		value.Mode = shadowverifier.RunMode(mode)
		values = append(values, value)
	}
	return values, rows.Err()
}

// RunFindings returns one run's durable divergences and counterexamples.
func (s *Store) RunFindings(ctx context.Context, runID int64) ([]shadowverifier.Finding, error) {
	rows, err := s.pool.Query(ctx, `
SELECT fact, kind, COALESCE(subject_role_id,''), COALESCE(subject_unit_id,''), COALESCE(capability_id,''), COALESCE(target_role_id,''),
       shadow_verdict, COALESCE(ground_verdict,''), detail, detected_at
FROM shadow_verifier_divergences WHERE run_id=$1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []shadowverifier.Finding
	for rows.Next() {
		var value shadowverifier.Finding
		var fact, kind string
		if err := rows.Scan(&fact, &kind, &value.SubjectRoleID, &value.SubjectUnitID, &value.CapabilityID, &value.TargetRoleID, &value.ShadowVerdict, &value.GroundVerdict, &value.Detail, &value.DetectedAt); err != nil {
			return nil, err
		}
		value.Fact = shadowverifier.FactID(fact)
		value.Kind = shadowverifier.FindingKind(kind)
		values = append(values, value)
	}
	return values, rows.Err()
}
