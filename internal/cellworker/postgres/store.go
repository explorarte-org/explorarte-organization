// Package postgres is the durable WorkSource for internal/cellworker: it
// selects invocation IDs pinned to a given execution principal that are
// still dispatchable, without owning any state of its own. Eligibility,
// claims, and dispatch quota remain enforced by Ramas 08-11 at dispatch
// time; this package only narrows the polling candidate set.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("cellworker store requires initialized PostgreSQL")
	}
	if strings.TrimSpace(organizationID) == "" {
		return nil, errors.New("cellworker store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

// ListEligible returns, at most limit, the lowest invocation IDs pinned to
// the active execution principal identified by principalKey that are still
// in a pre-dispatch status. It never returns invocations pinned to a
// different (or disabled) principal, and never returns legacy invocations
// with no pinned principal at all — those are reconciled separately (see
// orgctl model invocation reconcile), not picked up by a persistent worker.
func (s *Store) ListEligible(ctx context.Context, principalKey string, limit int) ([]int64, error) {
	principalKey = strings.TrimSpace(principalKey)
	if principalKey == "" {
		return nil, fmt.Errorf("cellworker: principal key is required")
	}
	if limit < 1 {
		return nil, fmt.Errorf("cellworker: limit must be positive")
	}
	rows, err := s.pool.Query(ctx, `
SELECT mi.id
FROM model_invocations mi
JOIN model_execution_principals p
    ON p.id = mi.execution_principal_id
   AND p.organization_id = mi.organization_id
WHERE mi.organization_id = $1
  AND p.principal_key = $2
  AND p.status = 'active'
  AND mi.status IN ('requested', 'claimed')
ORDER BY mi.id
LIMIT $3`, s.organizationID, principalKey, limit)
	if err != nil {
		return nil, fmt.Errorf("cellworker: list eligible invocations: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("cellworker: scan eligible invocation: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cellworker: list eligible invocations: %w", err)
	}
	return ids, nil
}
