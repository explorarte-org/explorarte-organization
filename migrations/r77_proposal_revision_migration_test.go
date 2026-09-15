package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration77ProposalRevisionLineageForwardBackForward(t *testing.T) {
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
	if err = testdbguard.RequireTestDatabase(ctx, dsn, pool); err != nil {
		t.Fatal(err)
	}

	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	var migration77, migration78 platformmigrations.Migration
	for _, migration := range loaded {
		if migration.Version == 77 {
			migration77 = migration
		}
		if migration.Version == 78 {
			migration78 = migration
		}
	}
	if migration77.Version != 77 {
		t.Fatal("migration 000077 is not present in the compiled set")
	}

	runner, err := platformmigrations.New(pool, rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Pending != 0 {
		t.Fatalf("status after up=%+v want ready with 0 pending", status)
	}

	columnExists := func(table, column string) bool {
		t.Helper()
		var exists bool
		query := "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name =  AND column_name = )"
		if err := pool.QueryRow(ctx, query, table, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}

	if !columnExists("campaign_proposals", "parent_proposal_id") ||
		!columnExists("campaign_proposals", "revision_number") ||
		!columnExists("campaign_proposals", "root_proposal_id") {
		t.Fatal("lineage columns must exist after up")
	}

	func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err = testdbguard.RequireDestructive(ctx, dsn, tx); err != nil {
			t.Fatal(err)
		}
		if migration78.Version == 78 {
			if _, err = tx.Exec(ctx, migration78.DownSQL); err != nil {
				t.Fatalf("down78: %v", err)
			}
			_, _ = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=78")
		}
		if _, err = tx.Exec(ctx, migration77.DownSQL); err != nil {
			t.Fatalf("down77: %v", err)
		}
		tag, err := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=77 AND name= AND checksum=", migration77.Name, migration77.Checksum)
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("deleted %d ledger rows, want 1", tag.RowsAffected())
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}()

	if columnExists("campaign_proposals", "parent_proposal_id") ||
		columnExists("campaign_proposals", "revision_number") ||
		columnExists("campaign_proposals", "root_proposal_id") {
		t.Fatal("lineage columns must NOT exist after down")
	}

	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !columnExists("campaign_proposals", "parent_proposal_id") ||
		!columnExists("campaign_proposals", "revision_number") ||
		!columnExists("campaign_proposals", "root_proposal_id") {
		t.Fatal("lineage columns must exist again after second up")
	}
}
