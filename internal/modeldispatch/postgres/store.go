package postgres

import (
	"context"
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("model dispatch store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

func withTx[T any](ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) (T, error)) (out T, err error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return out, mapError(err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(context.Background())
			panic(recovered)
		}
		if err != nil {
			_ = tx.Rollback(context.Background())
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

var _ modeldispatch.Store = (*Store)(nil)
