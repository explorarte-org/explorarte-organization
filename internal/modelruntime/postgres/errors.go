package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return modelruntime.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", modelruntime.ErrDatabaseUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if strings.HasPrefix(pgErr.Code, "08") {
			return fmt.Errorf("%w: PostgreSQL %s", modelruntime.ErrDatabaseUnavailable, pgErr.Code)
		}
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: PostgreSQL unique constraint", modelruntime.ErrConflict)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%w: PostgreSQL constraint %s", modelruntime.ErrInvalidRequest, pgErr.Code)
		case "40001", "40P01":
			return fmt.Errorf("%w: PostgreSQL %s", modelruntime.ErrConflict, pgErr.Code)
		}
		return fmt.Errorf("PostgreSQL %s: %w", pgErr.Code, err)
	}
	if pgconn.SafeToRetry(err) {
		return fmt.Errorf("%w: %v", modelruntime.ErrDatabaseUnavailable, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", modelruntime.ErrDatabaseUnavailable, err)
	}
	return err
}
