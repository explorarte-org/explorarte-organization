// Package testdbguard exists because of a real incident: on 2026-08-12 a
// delegated integration test trusted ORG_TEST_DATABASE_URL alone, a port
// collision on the shared production compose file silently redirected that
// URL at the development database instead of an isolated one, and a
// TRUNCATE ... CASCADE destroyed every org-scoped row of runtime history
// from several prior work phases. No backup existed.
//
// Every integration test that performs a destructive operation (TRUNCATE,
// migration DownSQL, direct schema_migrations mutation) must go through
// RequireDestructive before doing so. Every integration test that opens a
// database connection at all should go through RequireTestDatabase first.
// Neither function trusts anything the test itself injected into its own
// configuration (in particular, ORG_ENVIRONMENT=test is worthless as a
// safety signal when the test is the one setting it) — both independently
// verify, against the live connection, which database is actually on the
// other end.
package testdbguard

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

// CanonicalDisposableDatabase is the only PostgreSQL database name this
// package will ever treat as safe for integration tests, destructive or
// not. It is intentionally a single hardcoded constant, not something read
// from the environment — the whole point is that a bad DSN cannot talk this
// package into approving itself.
const CanonicalDisposableDatabase = "explorarte_test"

// DestructiveSentinelEnv must be set to exactly CanonicalDisposableDatabase
// before RequireDestructive will permit a destructive operation. It is a
// second, independently-set signal: RequireTestDatabase already proves the
// live connection really is CanonicalDisposableDatabase, and this sentinel
// additionally proves whoever launched the run made a deliberate,
// separately-authored decision to allow data loss on it. A single wrong
// value (the DSN) must never be sufficient authorization for TRUNCATE or
// migration DownSQL by itself.
const DestructiveSentinelEnv = "ORG_TEST_DESTRUCTIVE_DATABASE"

// queryRower is satisfied by *pgxpool.Pool. It exists so tests can verify
// the current_database() check without a live PostgreSQL server.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RequireTestDatabase verifies both that dsn names CanonicalDisposableDatabase
// and that the live connection behind pool is actually connected to it
// (via SELECT current_database()), not merely that the connection string
// claims to be. Call this before any integration test does anything with a
// database connection, destructive or not.
func RequireTestDatabase(ctx context.Context, dsn string, pool queryRower) error {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("testdbguard: parse database URL: %w", err)
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if name != CanonicalDisposableDatabase {
		return fmt.Errorf("testdbguard: connection string names database %q, only %q is permitted for integration tests", name, CanonicalDisposableDatabase)
	}
	if pool == nil {
		return fmt.Errorf("testdbguard: no live connection to verify current_database() against")
	}
	var observed string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&observed); err != nil {
		return fmt.Errorf("testdbguard: query current_database(): %w", err)
	}
	if observed != CanonicalDisposableDatabase {
		return fmt.Errorf("testdbguard: live connection reports database %q, only %q is permitted for integration tests", observed, CanonicalDisposableDatabase)
	}
	return nil
}

// RequireDestructive additionally requires DestructiveSentinelEnv to be
// deliberately set before permitting an operation such as TRUNCATE or
// migration DownSQL. Call this immediately before the destructive
// statement itself, not just once at test setup, so the check stays next
// to the operation it protects.
func RequireDestructive(ctx context.Context, dsn string, pool queryRower) error {
	if err := RequireTestDatabase(ctx, dsn, pool); err != nil {
		return err
	}
	if os.Getenv(DestructiveSentinelEnv) != CanonicalDisposableDatabase {
		return fmt.Errorf("testdbguard: destructive operation blocked, set %s=%s to explicitly authorize it", DestructiveSentinelEnv, CanonicalDisposableDatabase)
	}
	return nil
}
