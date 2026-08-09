// Package evaluationdb contains the fail-closed boundary shared by
// evaluation runners that mutate PostgreSQL. Synthetic fixtures are never
// allowed to run against a database whose name does not explicitly identify
// it as disposable.
package evaluationdb

import (
	"context"
	"fmt"
	"strings"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// RequireDisposable rejects nil stores and database names that do not carry
// an explicit test/fixture marker. This is deliberately based on the server's
// current_database(), not an environment variable or connection-string hint,
// so a misleading client configuration cannot bypass the guard.
func RequireDisposable(ctx context.Context, store *platformpostgres.Store) error {
	if store == nil || store.Pool() == nil {
		return fmt.Errorf("evaluation database is not configured")
	}
	var name string
	if err := store.Pool().QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return fmt.Errorf("resolve evaluation database identity: %w", err)
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if !strings.Contains(lower, "test") && !strings.Contains(lower, "fixture") && !strings.Contains(lower, "integration") {
		return fmt.Errorf("refusing stateful evaluation against database %q: its name must contain test, fixture, or integration", name)
	}
	return nil
}
