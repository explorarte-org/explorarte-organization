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
// Aggregations are strictly atomic via UNIQUE(organization_id, divergence_key) and ON CONFLICT DO UPDATE.
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
	obsAt := record.LastObservedAt
	if obsAt.IsZero() {
		obsAt = record.ObservedAt
	}
	if obsAt.IsZero() {
		obsAt = time.Now().UTC()
	}

	record.OrganizationID = orgID
	key := record.DivergenceKey
	if key == "" {
		key = ComputeDivergenceKey(record)
	}

	insertQuery := `
		INSERT INTO skill_provider_divergences (
			organization_id, divergence_key, role_id, skill_id, operation,
			field, primary_value, shadow_value, primary_version, shadow_version,
			primary_source_hash, shadow_source_hash, reason,
			first_observed_at, last_observed_at, observation_count
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14, 1)
		ON CONFLICT (organization_id, divergence_key)
		DO UPDATE SET
			last_observed_at = EXCLUDED.last_observed_at,
			observation_count = skill_provider_divergences.observation_count + 1`

	_, err := r.store.Pool().Exec(ctx, insertQuery,
		orgID,
		key,
		record.RoleID,
		record.SkillID,
		record.Operation,
		record.Field,
		record.PrimaryValue,
		record.ShadowValue,
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
		SELECT organization_id, divergence_key, role_id, COALESCE(skill_id, ''), operation,
		       COALESCE(field, ''), COALESCE(primary_value, ''), COALESCE(shadow_value, ''),
		       COALESCE(primary_version, ''), COALESCE(shadow_version, ''),
		       COALESCE(primary_source_hash, ''), COALESCE(shadow_source_hash, ''),
		       reason, first_observed_at, last_observed_at, observation_count
		FROM skill_provider_divergences
		WHERE organization_id = $1
		ORDER BY last_observed_at DESC
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
			&rec.DivergenceKey,
			&rec.RoleID,
			&rec.SkillID,
			&rec.Operation,
			&rec.Field,
			&rec.PrimaryValue,
			&rec.ShadowValue,
			&rec.PrimaryVersion,
			&rec.ShadowVersion,
			&rec.PrimarySourceHash,
			&rec.ShadowSourceHash,
			&rec.Reason,
			&rec.FirstObservedAt,
			&rec.LastObservedAt,
			&rec.ObservationCount,
		); err != nil {
			return nil, fmt.Errorf("scan divergence row: %w", err)
		}
		rec.ObservedAt = rec.LastObservedAt
		records = append(records, rec)
	}
	return records, rows.Err()
}
