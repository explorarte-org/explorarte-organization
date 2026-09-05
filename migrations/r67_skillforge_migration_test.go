package migrations_test

import (
	"context"
	"os"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration67ForwardBackForward(t *testing.T) {
	dsn := os.Getenv("ORG_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ORG_TEST_DATABASE_URL not set; skipping live postgres migration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := testdbguard.RequireDestructive(ctx, dsn, pool); err != nil {
		t.Fatalf("testdbguard authorization failed: %v", err)
	}

	upBytes, err := rootmigrations.Files.ReadFile("000067_create_skillforge.up.sql")
	if err != nil {
		t.Fatalf("read 000067 up sql: %v", err)
	}
	downBytes, err := rootmigrations.Files.ReadFile("000067_create_skillforge.down.sql")
	if err != nil {
		t.Fatalf("read 000067 down sql: %v", err)
	}

	tables := []string{
		"skill_provider_divergences",
		"skillforge_procedure_needs",
		"skill_source_materializations",
		"skillforge_runs",
		"skillforge_events",
		"skillforge_evaluations",
	}

	checkTablesExist := func(shouldExist bool) {
		for _, table := range tables {
			var exists bool
			err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists)
			if err != nil {
				t.Fatalf("check table %s: %v", table, err)
			}
			if exists != shouldExist {
				t.Fatalf("table %s: exists=%v, want %v", table, exists, shouldExist)
			}
		}
	}

	// 1. First FORWARD (old_tip -> new_tip)
	t.Log("Applying migration 67 UP (forward 1)...")
	if _, err := pool.Exec(ctx, string(upBytes)); err != nil {
		t.Fatalf("exec up 67: %v", err)
	}
	checkTablesExist(true)

	// 2. BACKWARD (new_tip -> old_tip)
	t.Log("Applying migration 67 DOWN (backward)...")
	if _, err := pool.Exec(ctx, string(downBytes)); err != nil {
		t.Fatalf("exec down 67: %v", err)
	}
	checkTablesExist(false)

	// 3. Second FORWARD (old_tip -> new_tip)
	t.Log("Applying migration 67 UP (forward 2)...")
	if _, err := pool.Exec(ctx, string(upBytes)); err != nil {
		t.Fatalf("exec up 67 second time: %v", err)
	}
	checkTablesExist(true)

	t.Log("MIGRATION_FORWARD_BACK_FORWARD = PASS")
}
