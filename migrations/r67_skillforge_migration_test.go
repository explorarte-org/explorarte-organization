package migrations_test

import (
	"context"
	"io/fs"
	"os"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration67ForwardBackForward(t *testing.T) {
	dsn := os.Getenv("ORG_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := testdbguard.RequireTestDatabase(ctx, dsn, pool); err != nil {
		t.Fatal(err)
	}
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) < 67 {
		t.Fatalf("compiled migration count=%d, want at least 67", len(loaded))
	}
	legacyFiles := fstest.MapFS{}
	entries, err := fs.ReadDir(rootmigrations.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) < 6 {
			continue
		}
		version, err := strconv.ParseInt(entry.Name()[:6], 10, 64)
		if err != nil || version > 67 {
			continue
		}
		body, err := fs.ReadFile(rootmigrations.Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		legacyFiles[entry.Name()] = &fstest.MapFile{Data: body}
	}
	runner, err := platformmigrations.New(pool, legacyFiles)
	if err != nil {
		t.Fatal(err)
	}
	if runner.Tip() != 67 {
		t.Fatalf("this rehearsal requires compiled tip 67, got %d", runner.Tip())
	}
	migration := loaded[66]
	tables := []string{"skill_provider_divergences", "skillforge_procedure_needs", "skill_source_materializations", "skillforge_runs", "skillforge_events", "skillforge_evaluations"}
	assertTip := func(want int64) {
		t.Helper()
		status, err := runner.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.Current != want || status.Applied != int(want) || status.Pending != 67-int(want) || status.Ready != (want == 67) {
			t.Fatalf("status=%+v want tip %d", status, want)
		}
		rows, err := pool.Query(ctx, "SELECT version,name,checksum FROM schema_migrations ORDER BY version")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var version int64
			var name, checksum string
			if err := rows.Scan(&version, &name, &checksum); err != nil {
				t.Fatal(err)
			}
			if count >= int(want) {
				t.Fatal("extra ledger row")
			}
			expected := loaded[count]
			if version != expected.Version || name != expected.Name || checksum != expected.Checksum {
				t.Fatalf("ledger row %d does not match embedded migration", version)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != int(want) {
			t.Fatalf("ledger rows=%d want %d", count, want)
		}
		for _, table := range tables {
			var exists bool
			if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != (want == 67) {
				t.Fatalf("table %s exists=%v at tip %d", table, exists, want)
			}
		}
		t.Logf("verified schema_migrations tip=%d, checksums and all six migration67 tables", want)
	}
	down := func() {
		t.Helper()
		assertTip(67)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err := testdbguard.RequireDestructive(ctx, dsn, tx); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, migration.DownSQL); err != nil {
			t.Fatalf("down67: %v", err)
		}
		tag, err := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=67 AND name=$1 AND checksum=$2", migration.Name, migration.Checksum)
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("deleted %d ledger rows, want 1", tag.RowsAffected())
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		assertTip(66)
	}
	// Prepare through the production runner; never ignore an initial rollback error.
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("prepare schema67: %v", err)
	}
	down()
	for pass := 1; pass <= 2; pass++ {
		result, err := runner.Up(ctx)
		if err != nil {
			t.Fatalf("forward %d: %v", pass, err)
		}
		if result.Current != 67 || len(result.Applied) != 1 || result.Applied[0] != 67 {
			t.Fatalf("forward %d result=%+v", pass, result)
		}
		assertTip(67)
		if pass == 1 {
			down()
		}
	}
}
