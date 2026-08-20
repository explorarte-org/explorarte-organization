package observe

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaTip reads the applied migration tip straight from the table the
// migration runner writes.
//
// COALESCE, not an empty-result error: a database with no migrations applied
// is at tip 0, which is a real and reportable state, and it is exactly the
// state a fresh deployment is in before anything has run.
type SchemaTip struct{ pool *pgxpool.Pool }

func NewSchemaTip(pool *pgxpool.Pool) *SchemaTip { return &SchemaTip{pool: pool} }

func (s *SchemaTip) DatabaseSchemaTip(ctx context.Context) (int64, error) {
	var tip int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&tip)
	return tip, err
}
