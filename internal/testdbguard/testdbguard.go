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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// CanonicalDisposableDatabase is the only PostgreSQL database name this
// package will ever treat as safe for integration tests, destructive or
// not. It is intentionally a single hardcoded constant, not something read
// from the environment — the whole point is that a bad DSN cannot talk this
// package into approving itself.
const CanonicalDisposableDatabase = "explorarte_test"

// freshDatabasePattern is the only other database name this package accepts: one FreshDatabase
// created, on the same server, from the canonical disposable database's authority, for exactly one
// test. The pattern is as fixed as the constant above -- a DSN cannot name its way into it except
// with a name only FreshDatabase generates.
var freshDatabasePattern = regexp.MustCompile(`^` + CanonicalDisposableDatabase + `_fresh_[0-9a-f]{12}$`)

func permittedDatabase(name string) bool {
	return name == CanonicalDisposableDatabase || freshDatabasePattern.MatchString(name)
}

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
	if !permittedDatabase(name) {
		return fmt.Errorf("testdbguard: connection string names database %q, only %q is permitted for integration tests", name, CanonicalDisposableDatabase)
	}
	if pool == nil {
		return fmt.Errorf("testdbguard: no live connection to verify current_database() against")
	}
	var observed string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&observed); err != nil {
		return fmt.Errorf("testdbguard: query current_database(): %w", err)
	}
	if !permittedDatabase(observed) || observed != name {
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

// FreshDatabase creates an empty database for one test, next to the canonical disposable database
// on the same server, and drops it when the test ends. It returns the new database's URL.
//
// Tests that reshape a schema (migration DownSQL, a schema reset, a migration ledger held at an old
// version) did it on the database every other integration package shares, and left it in a state
// the next package tripped on. Given its own database, such a test changes nothing another test
// can see. Creating it requires what a destructive operation requires on the canonical database:
// ORG_TEST_DATABASE_URL naming it, the live connection confirming it, and the destructive
// sentinel set. The pgvector extension is created, as the canonical database has it.
func FreshDatabase(t testing.TB) string {
	t.Helper()
	base := os.Getenv("ORG_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdbguard: connect to the canonical test database: %v", err)
	}
	defer admin.Close(context.Background())
	if err := RequireDestructive(ctx, base, admin); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}
	if name := strings.TrimPrefix(mustParse(t, base).Path, "/"); name != CanonicalDisposableDatabase {
		t.Fatalf("testdbguard: a fresh database is created only from %q, not %q", CanonicalDisposableDatabase, name)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := CanonicalDisposableDatabase + "_fresh_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("testdbguard: create %s: %v", name, err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), time.Minute)
		defer dropCancel()
		conn, err := pgx.Connect(dropCtx, base)
		if err != nil {
			t.Errorf("testdbguard: drop %s: %v", name, err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(dropCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("testdbguard: drop %s: %v", name, err)
		}
	})
	parsed := mustParse(t, base)
	parsed.Path = "/" + name
	fresh := parsed.String()
	conn, err := pgx.Connect(ctx, fresh)
	if err != nil {
		t.Fatalf("testdbguard: connect to %s: %v", name, err)
	}
	defer conn.Close(context.Background())
	if err := RequireTestDatabase(ctx, fresh, conn); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("testdbguard: create pgvector in %s: %v", name, err)
	}
	return fresh
}

func mustParse(t testing.TB, dsn string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("testdbguard: parse database URL: %v", err)
	}
	return parsed
}
