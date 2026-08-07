package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("model identity store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return modelidentity.ErrKeyNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return err
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23505":
			return fmt.Errorf("%w: PostgreSQL %s", modelidentity.ErrKeyConflict, pgerr.Code)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%w: PostgreSQL %s", modelidentity.ErrInvalidKey, pgerr.Code)
		case "40001", "40P01":
			return fmt.Errorf("%w: PostgreSQL %s", modelidentity.ErrConflict, pgerr.Code)
		}
	}
	return err
}

func withTx[T any](ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) (T, error)) (out T, err error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return out, mapError(err)
	}
	defer func() {
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

var _ modelidentity.Store = (*Store)(nil)
