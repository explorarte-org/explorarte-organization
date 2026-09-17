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

func TestMigration79CampaignPromotionsForwardBackForward(t *testing.T) {
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
	var migration79 platformmigrations.Migration
	for _, migration := range loaded {
		if migration.Version == 79 {
			migration79 = migration
			break
		}
	}
	if migration79.Version != 79 {
		t.Fatal("migration 000079 is not present in the compiled set")
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

	tableExists := func(table string) bool {
		t.Helper()
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass() IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}

	if !tableExists("campaign_promotions") {
		t.Fatal("campaign_promotions table must exist after up")
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
		if _, err = tx.Exec(ctx, migration79.DownSQL); err != nil {
			t.Fatalf("down79: %v", err)
		}
		tag, err := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=79 AND name= AND checksum=", migration79.Name, migration79.Checksum)
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

	if tableExists("campaign_promotions") {
		t.Fatal("campaign_promotions table must NOT exist after down")
	}

	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !tableExists("campaign_promotions") {
		t.Fatal("campaign_promotions table must exist again after second up")
	}
}
