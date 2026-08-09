// Package postgres is durable-but-ephemeral storage (migration 000033)
// for internal/webevidence.Evidence.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/webevidence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("webevidence store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("webevidence store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var _ webevidence.Store = (*Store)(nil)

func (s *Store) Save(ctx context.Context, evidence webevidence.Evidence) error {
	if evidence.OrganizationID != s.organizationID {
		return fmt.Errorf("%w: organization mismatch", webevidence.ErrInvalidEvidence)
	}
	if err := evidence.Validate(); err != nil {
		return err
	}
	chunks, err := json.Marshal(evidence.Chunks)
	if err != nil {
		return fmt.Errorf("webevidence store: marshal chunks: %w", err)
	}
	// A nil slice marshals to JSON null, not [] — the column requires a
	// JSON array (jsonb_typeof=array), so a nil/absent findings slice
	// (the common case: most pages have none) must still be encoded as an
	// empty array, never null.
	sanitizationFindings := evidence.SanitizationFindings
	if sanitizationFindings == nil {
		sanitizationFindings = []webevidence.SanitizationFinding{}
	}
	findings, err := json.Marshal(sanitizationFindings)
	if err != nil {
		return fmt.Errorf("webevidence store: marshal sanitization findings: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO web_evidence (id, organization_id, task_id, url, content_hash, captured_at, expires_at, chunks, sanitization_findings)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		evidence.ID, evidence.OrganizationID, evidence.TaskID, evidence.URL, evidence.ContentHash,
		evidence.CapturedAt.UTC(), evidence.ExpiresAt.UTC(), chunks, findings,
	); err != nil {
		return fmt.Errorf("webevidence store: save: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, organizationID, id string, now time.Time) (webevidence.Evidence, error) {
	if organizationID != s.organizationID || strings.TrimSpace(id) == "" {
		return webevidence.Evidence{}, fmt.Errorf("%w: invalid get request", webevidence.ErrInvalidEvidence)
	}
	row := s.pool.QueryRow(ctx, `
SELECT id, organization_id, task_id, url, content_hash, captured_at, expires_at, chunks, sanitization_findings
FROM web_evidence WHERE organization_id=$1 AND id=$2 AND expires_at > $3`, organizationID, id, now.UTC())
	evidence, err := scanEvidence(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return webevidence.Evidence{}, webevidence.ErrNotFound
		}
		return webevidence.Evidence{}, fmt.Errorf("webevidence store: get: %w", err)
	}
	return evidence, nil
}

func (s *Store) ListForTask(ctx context.Context, organizationID string, taskID int64, now time.Time) ([]webevidence.Evidence, error) {
	if organizationID != s.organizationID || taskID <= 0 {
		return nil, fmt.Errorf("%w: invalid list request", webevidence.ErrInvalidEvidence)
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, organization_id, task_id, url, content_hash, captured_at, expires_at, chunks, sanitization_findings
FROM web_evidence WHERE organization_id=$1 AND task_id=$2 AND expires_at > $3
ORDER BY captured_at DESC`, organizationID, taskID, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("webevidence store: list for task: %w", err)
	}
	defer rows.Close()
	var results []webevidence.Evidence
	for rows.Next() {
		evidence, err := scanEvidence(rows)
		if err != nil {
			return nil, fmt.Errorf("webevidence store: scan: %w", err)
		}
		results = append(results, evidence)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) Reap(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 10_000 {
		return 0, fmt.Errorf("%w: reap limit out of bounds", webevidence.ErrInvalidEvidence)
	}
	tag, err := s.pool.Exec(ctx, `
DELETE FROM web_evidence WHERE (organization_id, id) IN (
    SELECT organization_id, id FROM web_evidence WHERE organization_id=$1 AND expires_at <= $2 LIMIT $3
)`, s.organizationID, now.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("webevidence store: reap: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvidence(row rowScanner) (webevidence.Evidence, error) {
	var evidence webevidence.Evidence
	var chunksRaw, findingsRaw []byte
	if err := row.Scan(&evidence.ID, &evidence.OrganizationID, &evidence.TaskID, &evidence.URL, &evidence.ContentHash,
		&evidence.CapturedAt, &evidence.ExpiresAt, &chunksRaw, &findingsRaw); err != nil {
		return webevidence.Evidence{}, err
	}
	if err := json.Unmarshal(chunksRaw, &evidence.Chunks); err != nil {
		return webevidence.Evidence{}, fmt.Errorf("unmarshal chunks: %w", err)
	}
	if err := json.Unmarshal(findingsRaw, &evidence.SanitizationFindings); err != nil {
		return webevidence.Evidence{}, fmt.Errorf("unmarshal sanitization findings: %w", err)
	}
	return evidence, nil
}
