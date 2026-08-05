package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("authorization store requires an initialized PostgreSQL store")
	}
	return &Store{pool: store.Pool()}, nil
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

type scanner interface{ Scan(...any) error }

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return authorization.ErrRequestNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", authorization.ErrDatabaseUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("authorization conflict: PostgreSQL %s", pgErr.Code)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%w: PostgreSQL %s", authorization.ErrInvalidInput, pgErr.Code)
		case "57P01", "57P02", "57P03", "08000", "08001", "08003", "08004", "08006", "08007", "08P01":
			return fmt.Errorf("%w: PostgreSQL %s", authorization.ErrDatabaseUnavailable, pgErr.Code)
		}
	}
	if pgconn.SafeToRetry(err) || strings.Contains(strings.ToLower(err.Error()), "connection") {
		return fmt.Errorf("%w: %v", authorization.ErrDatabaseUnavailable, err)
	}
	return err
}

func (s *Store) GetOrganization(ctx context.Context, id string) (registry.Organization, error) {
	var value registry.Organization
	err := s.pool.QueryRow(ctx, `SELECT id,display_name,owner_role_id,ceo_role_id,runtime_target,state_store_target,cells_are_external,schema_version,document_status,COALESCE(current_revision_id,0),created_at,updated_at,retired_at FROM organizations WHERE id=$1`, id).Scan(
		&value.ID, &value.DisplayName, &value.OwnerRoleID, &value.CEORoleID, &value.RuntimeTarget, &value.StateStoreTarget, &value.CellsAreExternal, &value.SchemaVersion, &value.DocumentStatus, &value.CurrentRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.Organization{}, registry.ErrNotFound
	}
	return value, mapError(err)
}

func (s *Store) GetCurrentRevision(ctx context.Context, organizationID string) (*registry.Revision, error) {
	return getCurrentRevision(ctx, s.pool, organizationID)
}

func getCurrentRevision(ctx context.Context, db queryer, organizationID string) (*registry.Revision, error) {
	var value registry.Revision
	var schemaJSON, documentsJSON, countsJSON, diffJSON []byte
	err := db.QueryRow(ctx, `SELECT r.id,r.canonical_hash,r.previous_revision_id,r.status,r.schema_versions,r.document_hashes,r.counts,r.diff,r.applied_at FROM organizations o JOIN organization_registry_revisions r ON r.id=o.current_revision_id WHERE o.id=$1`, organizationID).Scan(
		&value.ID, &value.CanonicalHash, &value.PreviousRevisionID, &value.Status, &schemaJSON, &documentsJSON, &countsJSON, &diffJSON, &value.AppliedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, registry.ErrNotFound
	}
	if err != nil {
		return nil, mapError(err)
	}
	if err = json.Unmarshal(schemaJSON, &value.SchemaVersions); err != nil {
		return nil, fmt.Errorf("decode revision schema versions: %w", err)
	}
	if err = json.Unmarshal(documentsJSON, &value.DocumentHashes); err != nil {
		return nil, fmt.Errorf("decode revision document hashes: %w", err)
	}
	if err = json.Unmarshal(countsJSON, &value.Counts); err != nil {
		return nil, fmt.Errorf("decode revision counts: %w", err)
	}
	if err = json.Unmarshal(diffJSON, &value.Diff); err != nil {
		return nil, fmt.Errorf("decode revision diff: %w", err)
	}
	return &value, nil
}

const authorizationRoleColumns = `organization_id,id,unit_id,role_slug,display_name,runtime_kind,authority_class,canonical_leader,legacy_frontmatter_leader,model_policy,profile_path,department_agent_path,memory_domain,memory_path,summary,reports_to_source_text,rag_topics_source_text,source_status,enabled,executable,source_hash,source_revision_id,created_at,updated_at,retired_at`

func scanRole(row scanner) (registry.Role, error) {
	var value registry.Role
	err := row.Scan(&value.OrganizationID, &value.ID, &value.UnitID, &value.RoleSlug, &value.DisplayName, &value.RuntimeKind, &value.AuthorityClass, &value.CanonicalLeader, &value.LegacyFrontmatterLeader, &value.ModelPolicy, &value.ProfilePath, &value.DepartmentAgentPath, &value.MemoryDomain, &value.MemoryPath, &value.Summary, &value.ReportsToSourceText, &value.RAGTopicsSourceText, &value.SourceStatus, &value.Enabled, &value.Executable, &value.SourceHash, &value.SourceRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.Role{}, authorization.ErrRoleNotFound
	}
	return value, mapError(err)
}

func (s *Store) GetAuthorizationRole(ctx context.Context, organizationID, roleID string) (registry.Role, error) {
	return scanRole(s.pool.QueryRow(ctx, `SELECT `+authorizationRoleColumns+` FROM organization_roles WHERE organization_id=$1 AND id=$2`, organizationID, roleID))
}
