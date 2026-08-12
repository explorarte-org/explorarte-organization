//go:build integration

package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5"
)

func TestR21TransportAwareProviderOutcomeMigrationPostgreSQL17(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "4",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := platformpostgres.Open(ctx, cfg.Database, "r21-transport-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}

	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	tip := loaded[len(loaded)-1].Version
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != tip {
		t.Fatalf("migration current=%d want %d", result.Current, tip)
	}
	assertR21OutcomeSchema(t, ctx, store, true)

	if int64(len(loaded)) != tip || loaded[len(loaded)-1].Version != tip {
		t.Fatalf("loaded migrations=%d last=%d want %d/%d", len(loaded), loaded[len(loaded)-1].Version, tip, tip)
	}

	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing destructive migration DownSQL: %v", err)
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, execErr := tx.Exec(ctx, loaded[17].DownSQL); execErr != nil {
			return execErr
		}
		_, execErr := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=18`)
		return execErr
	}); err != nil {
		t.Fatalf("down 000018: %v", err)
	}
	assertR21OutcomeSchema(t, ctx, store, false)

	restored, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Applied) != 1 || restored.Applied[0] != 18 || restored.Current != tip {
		t.Fatalf("restored=%+v want only migration 18 reapplied, current=%d", restored, tip)
	}
	assertR21OutcomeSchema(t, ctx, store, true)
}

func assertR21OutcomeSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store, present bool) {
	t.Helper()
	var transport, exitCode *string
	if err := store.Pool().QueryRow(ctx, `
		SELECT
			(SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name='model_provider_outcomes' AND column_name='transport'),
			(SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name='model_provider_outcomes' AND column_name='process_exit_code')
	`).Scan(&transport, &exitCode); err != nil {
		t.Fatal(err)
	}
	if !present {
		if transport != nil || exitCode != nil {
			t.Fatalf("R21 columns remain after down: transport=%v exit=%v", transport, exitCode)
		}
		return
	}
	if transport == nil || *transport != "text" || exitCode == nil || *exitCode != "integer" {
		t.Fatalf("R21 columns missing/wrong: transport=%v exit=%v", transport, exitCode)
	}

	var triggerCount int
	if err := store.Pool().QueryRow(ctx, `
		SELECT count(*)
		FROM pg_trigger
		WHERE tgrelid='model_provider_outcomes'::regclass
		  AND NOT tgisinternal
		  AND tgname IN ('model_provider_outcomes_no_mutation','model_provider_outcomes_transport_derivation')
	`).Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if triggerCount != 2 {
		t.Fatalf("R21 outcome triggers=%d want 2", triggerCount)
	}

	rows, err := store.Pool().Query(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid='model_provider_outcomes'::regclass AND contype='c'
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var constraints strings.Builder
	for rows.Next() {
		var definition string
		if err := rows.Scan(&definition); err != nil {
			t.Fatal(err)
		}
		constraints.WriteString(definition)
		constraints.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	definition := constraints.String()
	for _, required := range []string{"cli_adapter", "process_exit_code", "transport"} {
		if !strings.Contains(definition, required) {
			t.Fatalf("R21 constraints do not mention %q:\n%s", required, definition)
		}
	}
}
