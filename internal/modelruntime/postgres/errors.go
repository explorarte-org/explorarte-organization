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
			// Naming the constraint is the difference between an
			// operator reading which rule was broken and inferring it
			// from the schema. AUTONOMY-SMOKE-017-R1 lost a campaign to
			// a check violation whose identity had to be reconstructed
			// by hand from the table definition and the adapter, when
			// the driver had carried the name all along. A constraint
			// name is a schema identifier, not data, so it discloses
			// nothing the error did not already imply.
			//
			// 22P02 is an invalid text representation and often has no
			// constraint at all, so the name is added only when the
			// driver actually reports one rather than invented.
			if name := strings.TrimSpace(pgErr.ConstraintName); name != "" {
				return fmt.Errorf("%w: PostgreSQL constraint %s (%s)", modelruntime.ErrInvalidRequest, pgErr.Code, name)
			}
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
