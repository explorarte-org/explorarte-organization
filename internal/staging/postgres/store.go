package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool              *pgxpool.Pool
	outboxMaxAttempts int
}

func New(store *platformpostgres.Store, outboxMaxAttempts int) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("staging store requires initialized PostgreSQL")
	}
	if outboxMaxAttempts < 1 || outboxMaxAttempts > 100 {
		return nil, errors.New("staging outbox attempts must be between 1 and 100")
	}
	return &Store{pool: store.Pool(), outboxMaxAttempts: outboxMaxAttempts}, nil
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
		return staging.ErrWorkspaceNotFound
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", staging.ErrDatabaseUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if strings.HasPrefix(pgErr.Code, "08") {
			return fmt.Errorf("%w: PostgreSQL %s", staging.ErrDatabaseUnavailable, pgErr.Code)
		}
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: PostgreSQL unique constraint", staging.ErrConflict)
		case "23503", "23514":
			return fmt.Errorf("%w: PostgreSQL constraint %s", staging.ErrInvalidInput, pgErr.Code)
		}
		return fmt.Errorf("PostgreSQL %s: %w", pgErr.Code, err)
	}
	if pgconn.SafeToRetry(err) {
		return fmt.Errorf("%w: %v", staging.ErrDatabaseUnavailable, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", staging.ErrDatabaseUnavailable, err)
	}
	return err
}

var _ staging.Persistence = (*Store)(nil)
