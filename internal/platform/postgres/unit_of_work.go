package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TxFunc func(context.Context, pgx.Tx) error

type UnitOfWork interface {
	WithinTransaction(context.Context, pgx.TxOptions, TxFunc) error
}

type unitOfWork struct {
	pool *pgxpool.Pool
}

func NewUnitOfWork(pool *pgxpool.Pool) UnitOfWork {
	return &unitOfWork{pool: pool}
}

func (u *unitOfWork) WithinTransaction(ctx context.Context, options pgx.TxOptions, fn TxFunc) (err error) {
	if u == nil || u.pool == nil {
		return errors.New("unit of work has no PostgreSQL pool")
	}
	if fn == nil {
		return errors.New("transaction function cannot be nil")
	}
	tx, err := u.pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = rollback(tx)
			panic(recovered)
		}
		if err != nil {
			if rollbackErr := rollback(tx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
			}
		}
	}()
	if err = fn(ctx, tx); err != nil {
		return fmt.Errorf("execute transaction: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func rollback(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}
