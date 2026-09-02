package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	// contextSnapshots is OPTIONAL and nil by default -- only
	// GetContextExecutionTelemetry needs it, and nothing in production
	// calls that method yet. Set via SetContextSnapshotReader once a real
	// caller exists; see that method's own doc comment for why this is a
	// setter rather than a required constructor argument.
	contextSnapshots modelruntime.ContextSnapshotSelectorReader
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("model runtime store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

// SetContextSnapshotReader wires the OPTIONAL capability
// GetContextExecutionTelemetry needs to read a context snapshot's
// ActorRoleID/TaskClass/ExecutionPurpose/ActorUnitID without this package
// ever querying the context-snapshot table directly. A setter, not a constructor
// parameter, because the other three New(...) call sites in this codebase
// (registry sync, orgctl's code runner) never call
// GetContextExecutionTelemetry and have no reason to construct or thread a
// context-engine reader through just to satisfy an unused parameter.
func (s *Store) SetContextSnapshotReader(reader modelruntime.ContextSnapshotSelectorReader) {
	s.contextSnapshots = reader
}

func lockInvocation(ctx context.Context, tx pgx.Tx, invocationID int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("model-invocation:%d", invocationID)); err != nil {
		return mapError(err)
	}
	return nil
}

func tryLockInvocation(ctx context.Context, tx pgx.Tx, invocationID int64) (bool, error) {
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("model-invocation:%d", invocationID)).Scan(&locked); err != nil {
		return false, mapError(err)
	}
	return locked, nil
}

type scanner interface{ Scan(...any) error }

func withTx[T any](ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) (T, error)) (out T, err error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return out, mapError(err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = rollback(tx)
			panic(recovered)
		}
		if err != nil {
			_ = rollback(tx)
		}
	}()
	out, err = fn(tx)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, mapError(err)
	}
	return out, nil
}
func rollback(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}

var _ modelruntime.Store = (*Store)(nil)
