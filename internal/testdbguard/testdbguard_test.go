package testdbguard

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeRow lets tests drive the current_database() branch without a live
// PostgreSQL server.
type fakeRow struct {
	value string
	err   error
}

func (f fakeRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	*(dest[0].(*string)) = f.value
	return nil
}

type fakeQuerier struct {
	row pgx.Row
}

func (f fakeQuerier) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return f.row
}

// Required negative test: DSN names explorarte_org (the real development
// database this package exists to protect) -> DENY.
func TestRequireTestDatabase_RejectsWrongDSNName(t *testing.T) {
	err := RequireTestDatabase(context.Background(), "postgres://user:pass@host:5432/explorarte_org?sslmode=disable", nil)
	if err == nil {
		t.Fatal("expected error for non-explorarte_test DSN, got nil")
	}
	if !strings.Contains(err.Error(), "explorarte_org") {
		t.Fatalf("expected error to name the rejected database, got: %v", err)
	}
}

// Required negative test: a plausible production-shaped database name that
// is NOT explorarte_org specifically -> DENY. This proves the check is an
// allowlist of exactly one name (CanonicalDisposableDatabase), not a
// blocklist of known-bad names -- a blocklist would miss this case.
func TestRequireTestDatabase_RejectsProductionLikeDatabaseName(t *testing.T) {
	pool := fakeQuerier{row: fakeRow{value: "explorarte_production"}}
	err := RequireTestDatabase(context.Background(), "postgres://user:pass@host:5432/explorarte_production?sslmode=disable", pool)
	if err == nil {
		t.Fatal("expected error for a production-shaped database name never explicitly blocklisted, got nil")
	}
}

func TestRequireTestDatabase_RejectsMalformedDSN(t *testing.T) {
	err := RequireTestDatabase(context.Background(), "://not a url", nil)
	if err == nil {
		t.Fatal("expected error for malformed DSN, got nil")
	}
}

func TestRequireTestDatabase_RejectsNilPoolEvenWithCorrectDSNName(t *testing.T) {
	err := RequireTestDatabase(context.Background(), "postgres://user:pass@host:5432/explorarte_test?sslmode=disable", nil)
	if err == nil {
		t.Fatal("expected error when there is no live connection to verify against, got nil")
	}
}

func TestRequireTestDatabase_RejectsWhenLiveDatabaseDiffersFromDSN(t *testing.T) {
	pool := fakeQuerier{row: fakeRow{value: "explorarte_org"}}
	err := RequireTestDatabase(context.Background(), "postgres://user:pass@host:5432/explorarte_test?sslmode=disable", pool)
	if err == nil {
		t.Fatal("expected error when current_database() disagrees with the DSN, got nil")
	}
	if !strings.Contains(err.Error(), "explorarte_org") {
		t.Fatalf("expected error to name the observed database, got: %v", err)
	}
}

func TestRequireTestDatabase_PassesWhenDSNAndLiveDatabaseBothMatch(t *testing.T) {
	pool := fakeQuerier{row: fakeRow{value: CanonicalDisposableDatabase}}
	err := RequireTestDatabase(context.Background(), "postgres://user:pass@host:5432/explorarte_test?sslmode=disable", pool)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// Required negative test: *_test database (correctly named and verified
// live), sentinel absent -> DENY. A correct database alone is never
// sufficient to authorize a destructive operation.
func TestRequireDestructive_BlocksWithoutSentinelEvenOnCorrectDatabase(t *testing.T) {
	t.Setenv(DestructiveSentinelEnv, "")
	pool := fakeQuerier{row: fakeRow{value: CanonicalDisposableDatabase}}
	err := RequireDestructive(context.Background(), "postgres://user:pass@host:5432/explorarte_test?sslmode=disable", pool)
	if err == nil {
		t.Fatal("expected error when destructive sentinel is unset, got nil")
	}
	if !strings.Contains(err.Error(), DestructiveSentinelEnv) {
		t.Fatalf("expected error to name the required sentinel, got: %v", err)
	}
}

// Required negative test: sentinel set correctly, but the live database is
// wrong -> DENY. The sentinel alone is never sufficient either; both
// independent conditions must hold simultaneously.
func TestRequireDestructive_BlocksOnWrongDatabaseEvenWithSentinelSet(t *testing.T) {
	t.Setenv(DestructiveSentinelEnv, CanonicalDisposableDatabase)
	pool := fakeQuerier{row: fakeRow{value: "explorarte_org"}}
	err := RequireDestructive(context.Background(), "postgres://user:pass@host:5432/explorarte_test?sslmode=disable", pool)
	if err == nil {
		t.Fatal("expected error: sentinel alone must never be sufficient without the database check also passing")
	}
}

// Required positive test: correct database (DSN + live current_database()
// both explorarte_test) AND the sentinel explicitly opted in -> ALLOW. This
// is the only combination that should ever pass.
func TestRequireDestructive_PassesWhenBothChecksPass(t *testing.T) {
	t.Setenv(DestructiveSentinelEnv, CanonicalDisposableDatabase)
	pool := fakeQuerier{row: fakeRow{value: CanonicalDisposableDatabase}}
	err := RequireDestructive(context.Background(), "postgres://user:pass@host:5432/explorarte_test?sslmode=disable", pool)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
