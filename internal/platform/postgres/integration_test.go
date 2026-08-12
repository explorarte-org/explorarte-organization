//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5"
)

func TestPostgresMigrationsAndUnitOfWork(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "4", "ORG_DATABASE_MIN_CONNS": "0"}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, cfg.Database, "explorarte-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing destructive schema reset: %v", err)
	}
	// A hand-maintained per-table/function DROP list here previously went
	// silently out of sync with the real migrations more than once — most
	// recently missing every table migration 000009 creates entirely, which
	// left runner.Up below unable to recreate them and corrupted this
	// database for every other integration suite sharing it, since this
	// test's DROP list ran destructively but incompletely. Recreating the
	// whole public schema is the only reset that can't drift from whatever
	// migrations actually exist, now or in the future.
	if _, err := store.Pool().Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset integration schema: %v", err)
	}
	// pgvector (R29) installs its types into the public schema by default —
	// the reset above drops them along with everything else, so migration
	// 000028 (the first to use the vector column type) would otherwise fail
	// here and for every integration package that shares this database and
	// runs after this one in the same `go test ./...` invocation. This is
	// a no-op if the extension is already present (e.g. run standalone
	// against a container that already has it).
	if _, err := store.Pool().Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector;`); err != nil {
		t.Fatalf("recreate pgvector extension after schema reset: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}

	// This test deliberately down-migrates individual middle versions and
	// deletes their schema_migrations rows. Runner.Up then re-applies only
	// those versions -- never the later ones, which are still recorded as
	// applied -- so any object a later migration altered is silently lost
	// while schema_migrations keeps claiming the schema is at tip. Migration
	// 000038 adds CHECK constraints to model_provider_outcomes, a table an
	// earlier migration recreates on the way back up, so those constraints
	// vanished and every integration package sharing this database after
	// this one inherited a schema that disagreed with its own version
	// record. Rebuild a pristine, fully-migrated schema on the way out so
	// the damage stays inside this test. This is a defer rather than a
	// t.Cleanup because the store pool is closed by a defer registered
	// earlier in this function, and cleanups run after every defer.
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancelCleanup()
		if _, cleanupErr := store.Pool().Exec(cleanupCtx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); cleanupErr != nil {
			t.Fatalf("restore schema after destructive migration test: %v", cleanupErr)
			return
		}
		if _, cleanupErr := store.Pool().Exec(cleanupCtx, `CREATE EXTENSION IF NOT EXISTS vector;`); cleanupErr != nil {
			t.Fatalf("restore pgvector after schema rebuild: %v", cleanupErr)
			return
		}
		if _, cleanupErr := runner.Up(cleanupCtx); cleanupErr != nil {
			t.Fatalf("re-apply migrations after schema rebuild: %v", cleanupErr)
		}
	}()
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loadedTip, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	tip := loadedTip[len(loadedTip)-1].Version
	if int64(len(result.Applied)) != tip || result.Current != tip {
		t.Fatalf("unexpected migration result: %+v want tip=%d", result, tip)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Pending != 0 || status.Applied != int(tip) || status.Current != tip || status.Latest != tip {
		t.Fatalf("status=%+v want tip=%d", status, tip)
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_events (event_type,actor_type,actor_id,payload) VALUES ($1,$2,$3,$4::jsonb)`, "integration.commit", "test", "runner", `{"ok":true}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	marker := errors.New("force rollback")
	err = store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (event_type) VALUES ($1)`, "integration.rollback"); err != nil {
			return err
		}
		return marker
	})
	if !errors.Is(err, marker) {
		t.Fatalf("rollback=%v", err)
	}
	var committed, rolled int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='integration.commit'),count(*) FILTER (WHERE event_type='integration.rollback') FROM audit_events`).Scan(&committed, &rolled); err != nil {
		t.Fatal(err)
	}
	if committed != 1 || rolled != 0 {
		t.Fatalf("counts=%d/%d", committed, rolled)
	}
	second, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Applied) != 0 || second.Current != tip {
		t.Fatalf("second=%+v want current=%d", second, tip)
	}
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(loaded)) != tip || loaded[len(loaded)-1].Version != tip {
		t.Fatalf("loaded=%d last=%d want %d/%d", len(loaded), loaded[len(loaded)-1].Version, tip, tip)
	}

	// Roll back migration 000019 first — it only adds constraints onto
	// tables migration 000017 creates, but must still be undone before 000017
	// itself, since 000017's down script drops those tables outright.
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[18].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=19`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000019: %v", err)
	}

	// First prove R21 can roll back to the exact post-R20 schema when no CLI
	// evidence exists.
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[17].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=18`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000018: %v", err)
	}
	var transportColumn *string
	if err := store.Pool().QueryRow(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name='model_provider_outcomes' AND column_name='transport'`).Scan(&transportColumn); err == nil || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("transport column still queryable after 000018 down: value=%v err=%v", transportColumn, err)
	}

	// R29's 000028 adds rag_chunk_embeddings/rag_embedding_batch_job_items,
	// both FK'd to rag_knowledge_chunks (000017) — it must come down before
	// 000017's DROP TABLE rag_knowledge_chunks, or that DROP fails outright
	// with "other objects depend on it". 000029 adds the generated
	// identifier_tokens column directly onto rag_knowledge_chunks — DROP
	// TABLE removes it fine either way, but its schema_migrations row must
	// also be deleted here, or Up() below skips reapplying it (it only
	// reapplies rows this block itself deleted) and every later query
	// against c.identifier_tokens breaks after 000017 comes back without
	// it. Both gaps are the same class already found and fixed for
	// 000021/000025/000030 in internal/modelruntime/postgres's own down/
	// reapply test — this file's version of that test predates R29 and
	// needed the same extension.
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[28].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=29`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000029: %v", err)
	}
	// R30's 000032 adds rag_chunk_embeddings_bge_m3, FK'd to
	// rag_knowledge_chunks exactly like 000028's rag_chunk_embeddings — same
	// gap, same fix: it must come down before 000017's DROP TABLE
	// rag_knowledge_chunks.
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[31].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=32`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000032: %v", err)
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[27].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=28`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000028: %v", err)
	}

	// Preserve the original R20 test: after R21 is gone, down 000017 and prove
	// the approved-knowledge tables disappear.
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[16].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=17`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000017: %v", err)
	}
	for _, table := range []string{"rag_knowledge_versions", "rag_knowledge_documents", "rag_knowledge_lifecycle_events"} {
		var relation *string
		if err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.' || $1)::text`, table).Scan(&relation); err != nil {
			t.Fatal(err)
		}
		if relation != nil {
			t.Fatalf("%s still exists", table)
		}
	}
	restored, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Up() walks migrations in ascending order, applying whichever versions
	// are missing from schema_migrations — 17/18/19/28/29/32 in that order
	// (20-27, 30, and 31 were never rolled back above, so Up() skips them).
	if len(restored.Applied) != 6 || restored.Applied[0] != 17 || restored.Applied[1] != 18 || restored.Applied[2] != 19 || restored.Applied[3] != 28 || restored.Applied[4] != 29 || restored.Applied[5] != 32 || restored.Current != tip {
		t.Fatalf("restore=%+v want 000017+000018+000019+000028+000029+000032", restored)
	}
	var identifierTokensExists bool
	if err := store.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='rag_knowledge_chunks' AND column_name='identifier_tokens')`).Scan(&identifierTokensExists); err != nil || !identifierTokensExists {
		t.Fatalf("rag_knowledge_chunks.identifier_tokens missing after reapply: exists=%v err=%v", identifierTokensExists, err)
	}
	var embeddingTableExists bool
	if err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.rag_chunk_embeddings') IS NOT NULL`).Scan(&embeddingTableExists); err != nil || !embeddingTableExists {
		t.Fatalf("rag_chunk_embeddings missing after reapply: exists=%v err=%v", embeddingTableExists, err)
	}
	var bgeM3TableExists bool
	if err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.rag_chunk_embeddings_bge_m3') IS NOT NULL`).Scan(&bgeM3TableExists); err != nil || !bgeM3TableExists {
		t.Fatalf("rag_chunk_embeddings_bge_m3 missing after reapply: exists=%v err=%v", bgeM3TableExists, err)
	}
}
