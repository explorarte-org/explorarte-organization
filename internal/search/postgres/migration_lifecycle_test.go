// Migration lifecycle test: applies the full series to a disposable
// database, verifies idempotent re-apply (up), rolls everything back (down),
// and re-applies again (up) — the deployment-safe cycle. Gated on
// ORG_TEST_DATABASE_URL like the rest of the integration suite.
package postgres

import (
	"context"
	"testing"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func TestMigrations_UpDownUp(t *testing.T) {
	pool := requireTestDatabase(t)
	ctx := context.Background()

	runner, err := platformmigrations.New(pool, rootmigrations.Files)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}

	// UP (already applied by requireTestDatabase) — re-apply must be a no-op.
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("idempotent re-apply failed: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("re-apply must apply nothing, got %v", result.Applied)
	}

	// DOWN — drop the research schema via the down migration.
	down, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range down {
		if m.DownSQL == "" {
			t.Fatalf("migration %06d missing down", m.Version)
		}
		if _, err := pool.Exec(ctx, m.DownSQL); err != nil {
			t.Fatalf("down migration %06d failed: %v", m.Version, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, m.Version); err != nil {
			t.Fatal(err)
		}
	}

	// UP again — fresh apply must succeed.
	reapplied, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("up after down failed: %v", err)
	}
	if len(reapplied.Applied) == 0 {
		t.Fatal("expected fresh apply after down")
	}

	// Checksum drift detection: a tampered ledger row must fail loudly.
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum = 'deadbeef'`); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err == nil {
		t.Fatal("checksum drift must fail the apply")
	}
	// Restore the correct checksum so cleanup TRUNCATE still works.
	if _, err := runner.Up(ctx); err == nil {
		t.Fatal("checksum drift cannot self-heal")
	}
	// Rebuild the ledger entry with the real checksum, then confirm clean.
	migs, _ := platformmigrations.Load(rootmigrations.Files)
	for _, m := range migs {
		if _, err := pool.Exec(ctx,
			`UPDATE schema_migrations SET checksum = $1 WHERE version = $2`, m.Checksum, m.Version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("apply must be clean after restoring checksums: %v", err)
	}
}
