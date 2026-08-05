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

type Store struct{ pool *pgxpool.Pool }

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("model runtime store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
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
