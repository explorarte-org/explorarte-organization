package contextprovider

import (
	"context"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// PostgresDivergenceRecorder persists metadata-only parity divergences durably into PostgreSQL.
// It never records SKILL.md bodies, prompts, context content, or credentials.
type PostgresDivergenceRecorder struct {
	store          *platformpostgres.Store
	organizationID string
}

func NewPostgresDivergenceRecorder(store *platformpostgres.Store, organizationID string) *PostgresDivergenceRecorder {
	return &PostgresDivergenceRecorder{
		store:          store,
		organizationID: organizationID,
	}
}

func (r *PostgresDivergenceRecorder) RecordDivergence(ctx context.Context, record DivergenceRecord) error {
	if r.store == nil || r.store.Pool() == nil {
		return fmt.Errorf("postgres store pool is nil")
	}
	orgID := record.OrganizationID
	if orgID == "" {
		orgID = r.organizationID
	}
	if orgID == "" {
		return fmt.Errorf("organization_id is required")
	}
	if strings.TrimSpace(record.RoleID) == "" {
		return fmt.Errorf("role_id is required")
	}
	if strings.TrimSpace(record.Operation) == "" {
		return fmt.Errorf("operation is required")
	}
	if strings.TrimSpace(record.Reason) == "" {
		return fmt.Errorf("reason is required")
	}
	obsAt := record.ObservedAt
	if obsAt.IsZero() {
		obsAt = time.Now().UTC()
	}

	// Deterministic deduplication:
	// If an identical divergence for (organization_id, role_id, skill_id, operation, reason)
	// was recorded in the last 10 seconds, skip inserting a duplicate row.
	cutoff := obsAt.Add(-10 * time.Second)
	var exists bool
	checkQuery := `
		SELECT EXISTS (
			SELECT 1 FROM skill_provider_divergences
			WHERE organization_id = $1
			  AND role_id = $2
			  AND COALESCE(skill_id, '') = COALESCE($3, '')
			  AND operation = $4
			  AND reason = $5
			  AND observed_at >= $6
		)`
	err := r.store.Pool().QueryRow(ctx, checkQuery, orgID, record.RoleID, record.SkillID, record.Operation, record.Reason, cutoff).Scan(&exists)
	if err == nil && exists {
		return nil
	}

	insertQuery := `
		INSERT INTO skill_provider_divergences (
			organization_id, role_id, skill_id, operation,
			primary_version, shadow_version, primary_source_hash, shadow_source_hash,
			reason, observed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err = r.store.Pool().Exec(ctx, insertQuery,
		orgID,
		record.RoleID,
		record.SkillID,
		record.Operation,
		record.PrimaryVersion,
		record.ShadowVersion,
		record.PrimarySourceHash,
		record.ShadowSourceHash,
		record.Reason,
		obsAt,
	)
	if err != nil {
		return fmt.Errorf("record skill provider divergence: %w", err)
	}
	return nil
}

func (r *PostgresDivergenceRecorder) ListDivergences(ctx context.Context, filters ...any) ([]DivergenceRecord, error) {
	if r.store == nil || r.store.Pool() == nil {
		return nil, fmt.Errorf("postgres store pool is nil")
	}
	orgID := r.organizationID
	limit := 100
	if len(filters) > 0 {
		if s, ok := filters[0].(string); ok && s != "" {
			orgID = s
		}
	}
	if len(filters) > 1 {
		if n, ok := filters[1].(int); ok && n > 0 {
			limit = n
		}
	}

	query := `
		SELECT organization_id, role_id, COALESCE(skill_id, ''), operation,
		       COALESCE(primary_version, ''), COALESCE(shadow_version, ''),
		       COALESCE(primary_source_hash, ''), COALESCE(shadow_source_hash, ''),
		       reason, observed_at
		FROM skill_provider_divergences
		WHERE organization_id = $1
		ORDER BY observed_at DESC
		LIMIT $2`

	rows, err := r.store.Pool().Query(ctx, query, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("query skill provider divergences: %w", err)
	}
	defer rows.Close()

	var records []DivergenceRecord
	for rows.Next() {
		var rec DivergenceRecord
		if err := rows.Scan(
			&rec.OrganizationID,
			&rec.RoleID,
			&rec.SkillID,
			&rec.Operation,
			&rec.PrimaryVersion,
			&rec.ShadowVersion,
			&rec.PrimarySourceHash,
			&rec.ShadowSourceHash,
			&rec.Reason,
			&rec.ObservedAt,
		); err != nil {
			return nil, fmt.Errorf("scan divergence row: %w", err)
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
