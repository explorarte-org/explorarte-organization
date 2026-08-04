package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const registryAdvisoryLockID int64 = 6093697772351380483

type PostgresRepository struct {
	pool *pgxpool.Pool
	uow  platformpostgres.UnitOfWork
}

type registryQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func NewPostgresRepository(store *platformpostgres.Store) (*PostgresRepository, error) {
	if store == nil || store.Pool() == nil || store.UnitOfWork() == nil {
		return nil, errors.New("organization registry requires an initialized PostgreSQL store")
	}
	return &PostgresRepository{pool: store.Pool(), uow: store.UnitOfWork()}, nil
}

func (r *PostgresRepository) GetOrganization(ctx context.Context, id string) (Organization, error) {
	return getOrganization(ctx, r.pool, id)
}

func getOrganization(ctx context.Context, db registryQueryer, id string) (Organization, error) {
	var value Organization
	err := db.QueryRow(ctx, `SELECT id,display_name,owner_role_id,ceo_role_id,runtime_target,state_store_target,cells_are_external,schema_version,document_status,COALESCE(current_revision_id,0),created_at,updated_at,retired_at FROM organizations WHERE id=$1 AND retired_at IS NULL`, id).Scan(
		&value.ID, &value.DisplayName, &value.OwnerRoleID, &value.CEORoleID, &value.RuntimeTarget, &value.StateStoreTarget, &value.CellsAreExternal, &value.SchemaVersion, &value.DocumentStatus, &value.CurrentRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt,
	)
	return value, mapQueryError(err)
}

func (r *PostgresRepository) ListUnits(ctx context.Context, organizationID string) ([]Unit, error) {
	return listUnits(ctx, r.pool, organizationID)
}

func listUnits(ctx context.Context, db registryQueryer, organizationID string) ([]Unit, error) {
	rows, err := db.Query(ctx, `SELECT organization_id,id,display_name,kind,operational,leaderless,leader_role_id,agent_path,schedule,timezone,canonical_status,source_revision_id,created_at,updated_at,retired_at FROM organizational_units WHERE organization_id=$1 AND retired_at IS NULL ORDER BY id`, organizationID)
	if err != nil {
		return nil, mapQueryError(err)
	}
	defer rows.Close()
	var values []Unit
	for rows.Next() {
		var value Unit
		if err := rows.Scan(&value.OrganizationID, &value.ID, &value.DisplayName, &value.Kind, &value.Operational, &value.Leaderless, &value.LeaderRoleID, &value.AgentPath, &value.Schedule, &value.Timezone, &value.CanonicalStatus, &value.SourceRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt); err != nil {
			return nil, mapQueryError(err)
		}
		values = append(values, value)
	}
	return values, mapQueryError(rows.Err())
}

func (r *PostgresRepository) GetUnit(ctx context.Context, organizationID, unitID string) (Unit, error) {
	return getUnit(ctx, r.pool, organizationID, unitID)
}

func getUnit(ctx context.Context, db registryQueryer, organizationID, unitID string) (Unit, error) {
	var value Unit
	err := db.QueryRow(ctx, `SELECT organization_id,id,display_name,kind,operational,leaderless,leader_role_id,agent_path,schedule,timezone,canonical_status,source_revision_id,created_at,updated_at,retired_at FROM organizational_units WHERE organization_id=$1 AND id=$2 AND retired_at IS NULL`, organizationID, unitID).Scan(&value.OrganizationID, &value.ID, &value.DisplayName, &value.Kind, &value.Operational, &value.Leaderless, &value.LeaderRoleID, &value.AgentPath, &value.Schedule, &value.Timezone, &value.CanonicalStatus, &value.SourceRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt)
	return value, mapQueryError(err)
}

const roleColumns = `organization_id,id,unit_id,role_slug,display_name,runtime_kind,authority_class,canonical_leader,legacy_frontmatter_leader,model_policy,profile_path,department_agent_path,memory_domain,memory_path,summary,reports_to_source_text,rag_topics_source_text,source_status,enabled,executable,source_hash,source_revision_id,created_at,updated_at,retired_at`

func scanRole(row interface{ Scan(...any) error }) (Role, error) {
	var value Role
	err := row.Scan(&value.OrganizationID, &value.ID, &value.UnitID, &value.RoleSlug, &value.DisplayName, &value.RuntimeKind, &value.AuthorityClass, &value.CanonicalLeader, &value.LegacyFrontmatterLeader, &value.ModelPolicy, &value.ProfilePath, &value.DepartmentAgentPath, &value.MemoryDomain, &value.MemoryPath, &value.Summary, &value.ReportsToSourceText, &value.RAGTopicsSourceText, &value.SourceStatus, &value.Enabled, &value.Executable, &value.SourceHash, &value.SourceRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt)
	return value, mapQueryError(err)
}

func (r *PostgresRepository) GetRole(ctx context.Context, organizationID, roleID string) (Role, error) {
	return getRole(ctx, r.pool, organizationID, roleID)
}

func getRole(ctx context.Context, db registryQueryer, organizationID, roleID string) (Role, error) {
	return scanRole(db.QueryRow(ctx, `SELECT `+roleColumns+` FROM organization_roles WHERE organization_id=$1 AND id=$2 AND retired_at IS NULL`, organizationID, roleID))
}

func (r *PostgresRepository) ListRoles(ctx context.Context, organizationID string, filter RoleFilter) ([]Role, error) {
	return listRoles(ctx, r.pool, organizationID, filter)
}

func listRoles(ctx context.Context, db registryQueryer, organizationID string, filter RoleFilter) ([]Role, error) {
	query := `SELECT ` + roleColumns + ` FROM organization_roles WHERE organization_id=$1 AND retired_at IS NULL`
	args := []any{organizationID}
	if filter.UnitID != "" {
		args = append(args, filter.UnitID)
		query += fmt.Sprintf(" AND unit_id=$%d", len(args))
	}
	if filter.EnabledOnly {
		query += ` AND enabled`
	}
	query += ` ORDER BY id`
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, mapQueryError(err)
	}
	defer rows.Close()
	var values []Role
	for rows.Next() {
		value, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, mapQueryError(rows.Err())
}

func (r *PostgresRepository) GetLeader(ctx context.Context, organizationID, unitID string) (Role, error) {
	return getLeader(ctx, r.pool, organizationID, unitID)
}

func getLeader(ctx context.Context, db registryQueryer, organizationID, unitID string) (Role, error) {
	return scanRole(db.QueryRow(ctx, `SELECT `+roleColumns+` FROM organization_roles WHERE organization_id=$1 AND unit_id=$2 AND canonical_leader AND retired_at IS NULL`, organizationID, unitID))
}

func (r *PostgresRepository) ListWorkers(ctx context.Context, organizationID, unitID string) ([]Role, error) {
	return listWorkers(ctx, r.pool, organizationID, unitID)
}

func listWorkers(ctx context.Context, db registryQueryer, organizationID, unitID string) ([]Role, error) {
	rows, err := db.Query(ctx, `SELECT `+roleColumns+` FROM organization_roles WHERE organization_id=$1 AND unit_id=$2 AND NOT canonical_leader AND retired_at IS NULL ORDER BY id`, organizationID, unitID)
	if err != nil {
		return nil, mapQueryError(err)
	}
	defer rows.Close()
	var values []Role
	for rows.Next() {
		value, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, mapQueryError(rows.Err())
}

func (r *PostgresRepository) GetCurrentRevision(ctx context.Context, organizationID string) (*Revision, error) {
	return getCurrentRevision(ctx, r.pool, organizationID)
}

func getCurrentRevision(ctx context.Context, db registryQueryer, organizationID string) (*Revision, error) {
	var revisionID int64
	err := db.QueryRow(ctx, `SELECT COALESCE(current_revision_id,0) FROM organizations WHERE id=$1 AND retired_at IS NULL`, organizationID).Scan(&revisionID)
	if err != nil {
		return nil, mapQueryError(err)
	}
	if revisionID == 0 {
		return nil, nil
	}
	return readRevision(ctx, db, revisionID)
}

func readRevision(ctx context.Context, db registryQueryer, id int64) (*Revision, error) {
	var value Revision
	var previousRevisionID int64
	var schema, documents, counts, diff []byte
	err := db.QueryRow(ctx, `SELECT id,canonical_hash,COALESCE(previous_revision_id,0),status,schema_versions,document_hashes,counts,diff,applied_at FROM organization_registry_revisions WHERE id=$1`, id).Scan(&value.ID, &value.CanonicalHash, &previousRevisionID, &value.Status, &schema, &documents, &counts, &diff, &value.AppliedAt)
	if err != nil {
		return nil, mapQueryError(err)
	}
	if err = json.Unmarshal(schema, &value.SchemaVersions); err != nil {
		return nil, fmt.Errorf("decode revision schema versions: %w", err)
	}
	if err = json.Unmarshal(documents, &value.DocumentHashes); err != nil {
		return nil, fmt.Errorf("decode revision document hashes: %w", err)
	}
	if err = json.Unmarshal(counts, &value.Counts); err != nil {
		return nil, fmt.Errorf("decode revision counts: %w", err)
	}
	if err = json.Unmarshal(diff, &value.Diff); err != nil {
		return nil, fmt.Errorf("decode revision diff: %w", err)
	}
	if previousRevisionID != 0 {
		value.PreviousRevisionID = &previousRevisionID
		if err := db.QueryRow(ctx, `SELECT canonical_hash FROM organization_registry_revisions WHERE id=$1`, previousRevisionID).Scan(&value.PreviousHash); err != nil {
			return nil, mapQueryError(err)
		}
	}
	return &value, nil
}

func (r *PostgresRepository) LoadCurrentSnapshot(ctx context.Context, organizationID string) (*Snapshot, error) {
	return loadCurrentSnapshot(ctx, r.pool, organizationID)
}

func loadCurrentSnapshot(ctx context.Context, db registryQueryer, organizationID string) (*Snapshot, error) {
	organization, err := getOrganization(ctx, db, organizationID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	units, err := listUnits(ctx, db, organizationID)
	if err != nil {
		return nil, err
	}
	roles, err := listRoles(ctx, db, organizationID, RoleFilter{})
	if err != nil {
		return nil, err
	}
	var lines []ReportingLine
	if organization.CurrentRevision != 0 {
		rows, err := db.Query(ctx, `SELECT organization_id,role_id,reports_to_role_id,relationship,revision_id,recorded_at FROM organization_reporting_lines WHERE organization_id=$1 AND revision_id=$2 ORDER BY role_id,reports_to_role_id,relationship`, organizationID, organization.CurrentRevision)
		if err != nil {
			return nil, mapQueryError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var line ReportingLine
			if err = rows.Scan(&line.OrganizationID, &line.RoleID, &line.ReportsToRoleID, &line.Relationship, &line.RevisionID, &line.RecordedAt); err != nil {
				return nil, mapQueryError(err)
			}
			lines = append(lines, line)
		}
		if err = rows.Err(); err != nil {
			return nil, mapQueryError(err)
		}
	}
	revision, err := getCurrentRevision(ctx, db, organizationID)
	if err != nil {
		return nil, err
	}
	snapshot := &Snapshot{Organization: organization, Units: units, Roles: roles, ReportingLines: lines}
	snapshot.Counts = countSnapshot(*snapshot)
	if revision != nil {
		snapshot.CanonicalHash = revision.CanonicalHash
		snapshot.Documents = make([]DocumentDigest, 0, len(revision.DocumentHashes))
		for path, hash := range revision.DocumentHashes {
			snapshot.Documents = append(snapshot.Documents, DocumentDigest{Path: path, SchemaVersion: revision.SchemaVersions[path], SemanticHash: hash})
		}
		sort.Slice(snapshot.Documents, func(i, j int) bool { return snapshot.Documents[i].Path < snapshot.Documents[j].Path })
	}
	return snapshot, nil
}

// Apply materializes one canonical snapshot atomically. The database comparison
// and diff are recalculated under the same serializable transaction and advisory
// lock that writes the revision, avoiding stale administrative dry-run results.
func (r *PostgresRepository) Apply(ctx context.Context, canonical Snapshot) (result SyncResult, err error) {
	result.CanonicalHash = canonical.CanonicalHash
	err = r.uow.WithinTransaction(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, registryAdvisoryLockID); err != nil {
			return fmt.Errorf("acquire registry lock: %w", err)
		}
		current, err := loadCurrentSnapshot(ctx, tx, canonical.Organization.ID)
		if err != nil {
			return err
		}
		diff := CompareSnapshots(current, canonical)
		result.Diff = diff
		var previousID int64
		if current != nil {
			previousID = current.Organization.CurrentRevision
			result.PreviousHash = current.CanonicalHash
			if current.CanonicalHash == canonical.CanonicalHash && !diff.HasChanges() {
				result.NoOp = true
				return nil
			}
		}

		schemaJSON, err := json.Marshal(schemaVersions(canonical))
		if err != nil {
			return fmt.Errorf("encode schema versions: %w", err)
		}
		documentJSON, err := json.Marshal(documentHashes(canonical))
		if err != nil {
			return fmt.Errorf("encode document hashes: %w", err)
		}
		countsJSON, err := json.Marshal(canonical.Counts)
		if err != nil {
			return fmt.Errorf("encode registry counts: %w", err)
		}
		diffJSON, err := json.Marshal(diff)
		if err != nil {
			return fmt.Errorf("encode registry diff: %w", err)
		}
		var revisionID int64
		if err = tx.QueryRow(ctx, `INSERT INTO organization_registry_revisions(canonical_hash,previous_revision_id,status,schema_versions,document_hashes,counts,diff) VALUES($1,$2,'applied',$3,$4,$5,$6) RETURNING id`, canonical.CanonicalHash, nullableInt64(previousID), schemaJSON, documentJSON, countsJSON, diffJSON).Scan(&revisionID); err != nil {
			return err
		}
		for _, document := range canonical.Documents {
			if _, err = tx.Exec(ctx, `INSERT INTO organization_registry_revision_documents(revision_id,document_path,schema_version,document_status,semantic_hash) VALUES($1,$2,$3,$4,$5)`, revisionID, document.Path, nullString(document.SchemaVersion), nullString(document.DocumentStatus), document.SemanticHash); err != nil {
				return err
			}
		}
		organization := canonical.Organization
		if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,display_name,owner_role_id,ceo_role_id,runtime_target,state_store_target,cells_are_external,schema_version,document_status,current_revision_id,retired_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULL) ON CONFLICT(id) DO UPDATE SET display_name=EXCLUDED.display_name,owner_role_id=EXCLUDED.owner_role_id,ceo_role_id=EXCLUDED.ceo_role_id,runtime_target=EXCLUDED.runtime_target,state_store_target=EXCLUDED.state_store_target,cells_are_external=EXCLUDED.cells_are_external,schema_version=EXCLUDED.schema_version,document_status=EXCLUDED.document_status,current_revision_id=EXCLUDED.current_revision_id,updated_at=NOW(),retired_at=NULL`, organization.ID, organization.DisplayName, organization.OwnerRoleID, organization.CEORoleID, organization.RuntimeTarget, organization.StateStoreTarget, organization.CellsAreExternal, organization.SchemaVersion, organization.DocumentStatus, revisionID); err != nil {
			return err
		}
		for _, unit := range canonical.Units {
			if _, err = tx.Exec(ctx, `INSERT INTO organizational_units(organization_id,id,display_name,kind,operational,leaderless,leader_role_id,agent_path,schedule,timezone,canonical_status,source_revision_id,retired_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULL) ON CONFLICT(organization_id,id) DO UPDATE SET display_name=EXCLUDED.display_name,kind=EXCLUDED.kind,operational=EXCLUDED.operational,leaderless=EXCLUDED.leaderless,leader_role_id=EXCLUDED.leader_role_id,agent_path=EXCLUDED.agent_path,schedule=EXCLUDED.schedule,timezone=EXCLUDED.timezone,canonical_status=EXCLUDED.canonical_status,source_revision_id=EXCLUDED.source_revision_id,updated_at=NOW(),retired_at=NULL`, unit.OrganizationID, unit.ID, unit.DisplayName, unit.Kind, unit.Operational, unit.Leaderless, unit.LeaderRoleID, unit.AgentPath, unit.Schedule, unit.Timezone, unit.CanonicalStatus, revisionID); err != nil {
				return err
			}
		}
		for _, role := range canonical.Roles {
			if _, err = tx.Exec(ctx, `INSERT INTO organization_roles(organization_id,id,unit_id,role_slug,display_name,runtime_kind,authority_class,canonical_leader,legacy_frontmatter_leader,model_policy,profile_path,department_agent_path,memory_domain,memory_path,summary,reports_to_source_text,rag_topics_source_text,source_status,enabled,executable,source_hash,source_revision_id,retired_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,NULL) ON CONFLICT(organization_id,id) DO UPDATE SET unit_id=EXCLUDED.unit_id,role_slug=EXCLUDED.role_slug,display_name=EXCLUDED.display_name,runtime_kind=EXCLUDED.runtime_kind,authority_class=EXCLUDED.authority_class,canonical_leader=EXCLUDED.canonical_leader,legacy_frontmatter_leader=EXCLUDED.legacy_frontmatter_leader,model_policy=EXCLUDED.model_policy,profile_path=EXCLUDED.profile_path,department_agent_path=EXCLUDED.department_agent_path,memory_domain=EXCLUDED.memory_domain,memory_path=EXCLUDED.memory_path,summary=EXCLUDED.summary,reports_to_source_text=EXCLUDED.reports_to_source_text,rag_topics_source_text=EXCLUDED.rag_topics_source_text,source_status=EXCLUDED.source_status,enabled=EXCLUDED.enabled,executable=EXCLUDED.executable,source_hash=EXCLUDED.source_hash,source_revision_id=EXCLUDED.source_revision_id,updated_at=NOW(),retired_at=NULL`, role.OrganizationID, role.ID, role.UnitID, role.RoleSlug, role.DisplayName, role.RuntimeKind, role.AuthorityClass, role.CanonicalLeader, role.LegacyFrontmatterLeader, role.ModelPolicy, role.ProfilePath, role.DepartmentAgentPath, role.MemoryDomain, role.MemoryPath, role.Summary, role.ReportsToSourceText, role.RAGTopicsSourceText, role.SourceStatus, role.Enabled, role.Executable, role.SourceHash, revisionID); err != nil {
				return err
			}
		}
		for _, id := range diff.Units.Retired {
			if _, err = tx.Exec(ctx, `UPDATE organizational_units SET retired_at=COALESCE(retired_at,NOW()),updated_at=NOW(),source_revision_id=$3 WHERE organization_id=$1 AND id=$2`, organization.ID, id, revisionID); err != nil {
				return err
			}
		}
		for _, id := range diff.Roles.Retired {
			if _, err = tx.Exec(ctx, `UPDATE organization_roles SET retired_at=COALESCE(retired_at,NOW()),enabled=false,executable=false,updated_at=NOW(),source_revision_id=$3 WHERE organization_id=$1 AND id=$2`, organization.ID, id, revisionID); err != nil {
				return err
			}
		}
		for _, line := range canonical.ReportingLines {
			if _, err = tx.Exec(ctx, `INSERT INTO organization_reporting_lines(revision_id,organization_id,role_id,reports_to_role_id,relationship) VALUES($1,$2,$3,$4,$5)`, revisionID, line.OrganizationID, line.RoleID, line.ReportsToRoleID, line.Relationship); err != nil {
				return err
			}
		}
		added, updated, retired := diff.Totals()
		payload, err := json.Marshal(map[string]any{
			"revision_hash":     canonical.CanonicalHash,
			"previous_revision": result.PreviousHash,
			"organizations":     canonical.Counts.Organizations,
			"units":             canonical.Counts.Units,
			"roles":             canonical.Counts.Roles,
			"relations":         canonical.Counts.ReportingRelationships,
			"added":             added,
			"updated":           updated,
			"retired":           retired,
		})
		if err != nil {
			return fmt.Errorf("encode registry audit payload: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES('organization.registry_synced','system','orgctl','organization',$1,$2::jsonb)`, organization.ID, payload); err != nil {
			return err
		}
		revision, err := readRevision(ctx, tx, revisionID)
		if err != nil {
			return err
		}
		result.Applied = true
		result.Revision = revision
		return nil
	})
	if err != nil {
		return SyncResult{}, mapQueryError(err)
	}
	return result, nil
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if IsDatabaseUnavailable(err) {
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("PostgreSQL %s: %w", pgErr.Code, err)
	}
	return err
}

// IsDatabaseUnavailable classifies transport and PostgreSQL connection failures
// without treating domain validation or SQL constraint failures as availability
// incidents. CLI callers use it to return the dedicated database exit code.
func IsDatabaseUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDatabaseUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "08") {
		return true
	}
	if pgconn.SafeToRetry(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

var _ Repository = (*PostgresRepository)(nil)
