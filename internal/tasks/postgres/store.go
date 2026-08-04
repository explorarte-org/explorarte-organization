package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("durable task store requires an initialized PostgreSQL store")
	}
	return &Store{pool: store.Pool()}, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type scanner interface{ Scan(...any) error }

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.ErrNotFound
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", tasks.ErrDatabaseUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if strings.HasPrefix(pgErr.Code, "08") {
			return fmt.Errorf("%w: PostgreSQL %s", tasks.ErrDatabaseUnavailable, pgErr.Code)
		}
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return fmt.Errorf("%w: PostgreSQL %s", tasks.ErrConflict, pgErr.Code)
		}
		return fmt.Errorf("PostgreSQL %s: %w", pgErr.Code, err)
	}
	if pgconn.SafeToRetry(err) {
		return fmt.Errorf("%w: %v", tasks.ErrDatabaseUnavailable, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", tasks.ErrDatabaseUnavailable, err)
	}
	return err
}

var _ tasks.Persistence = (*Store)(nil)
