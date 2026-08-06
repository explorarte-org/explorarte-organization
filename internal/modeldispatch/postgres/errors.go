package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return modeldispatch.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", modeldispatch.ErrDatabaseUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			if pgErr.ConstraintName == "model_dispatcher_assignments_one_active_idx" {
				return fmt.Errorf("%w: PostgreSQL unique constraint", modeldispatch.ErrAssignmentConflict)
			}
			return fmt.Errorf("%w: PostgreSQL unique constraint", modeldispatch.ErrConflict)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%w: PostgreSQL constraint %s", modeldispatch.ErrInvalidRequest, pgErr.Code)
		}
		return fmt.Errorf("PostgreSQL %s: %w", pgErr.Code, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", modeldispatch.ErrDatabaseUnavailable, err)
	}
	return err
}
