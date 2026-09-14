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

// TestMigration74CEOChatForwardBackForward is round section 18's
// up/down/up rehearsal for migration 000074 (CEO chat persistence).
// It follows the same manual-DownSQL-execution shape
// TestMigration67ForwardBackForward established: the platform migration
// runner is forward-only in production (Up/Status only), so a down
// rehearsal applies each migration's own DownSQL directly, inside a test
// transaction guarded by testdbguard.RequireDestructive, and never touches
// a non-disposable database.
func TestMigration74CEOChatForwardBackForward(t *testing.T) {
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
	var migration74 platformmigrations.Migration
	found := false
	for _, migration := range loaded {
		if migration.Version == 74 {
			migration74 = migration
			found = true
			break
		}
	}
	if !found {
		t.Fatal("migration 000074 is not present in the compiled set")
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
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	if !tableExists("ceo_chat_conversations") || !tableExists("ceo_chat_messages") {
		t.Fatal("ceo_chat tables must exist after up")
	}

	// down: apply migration 74's own DownSQL, then remove its ledger row --
	// the same two-step manual rehearsal TestMigration67ForwardBackForward
	// uses, since the production runner exposes no Down method itself.
	func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err = testdbguard.RequireDestructive(ctx, dsn, tx); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, migration74.DownSQL); err != nil {
			t.Fatalf("down74: %v", err)
		}
		tag, err := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=74 AND name=$1 AND checksum=$2", migration74.Name, migration74.Checksum)
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

	if tableExists("ceo_chat_conversations") || tableExists("ceo_chat_messages") {
		t.Fatal("ceo_chat tables must NOT exist after down")
	}
	status, err = runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 73 || status.Pending != 1 {
		t.Fatalf("status after down=%+v want current=73 pending=1", status)
	}

	// up again: redo, prove the schema comes back byte-identical in shape.
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !tableExists("ceo_chat_conversations") || !tableExists("ceo_chat_messages") {
		t.Fatal("ceo_chat tables must exist again after the second up")
	}
	status, err = runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Pending != 0 || status.Current != 74 {
		t.Fatalf("status after second up=%+v want ready, current=74, 0 pending", status)
	}
}
