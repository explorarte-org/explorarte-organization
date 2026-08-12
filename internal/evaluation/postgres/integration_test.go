//go:build integration

package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphfixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	evaluationpostgres "github.com/Mireuz13/explorarte-organization/internal/evaluation/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const evaluationIntegrationOrganization = "explorarte"

func TestEvaluationPostgresStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	platform := openEvaluationStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetEvaluationSchema(t, ctx, platform)
	t.Cleanup(func() { resetEvaluationSchema(t, context.Background(), platform) })
	syncEvaluationCanonical(t, ctx, platform)

	store, err := evaluationpostgres.New(platform, evaluationIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	runID, err := store.CreateRun(ctx, "r30", "decisiongraph", "integration/worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if runID <= 0 {
		t.Fatalf("run id=%d", runID)
	}

	runner2 := decisiongraphfixtures.DecisionGraphRunner{}
	activated := decisiongraphfixtures.Activate(fixtures.CatalogR30())
	outcomes, err := fixtures.RunSuite(ctx, runner2, activated, "decisiongraph")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) == 0 {
		t.Fatal("expected at least one outcome from runner-ready fixtures")
	}
	for _, outcome := range outcomes {
		if err := store.RecordOutcome(ctx, runID, outcome, now); err != nil {
			t.Fatalf("record outcome for %s: %v", outcome.FixtureID, err)
		}
	}
	if err := store.CompleteRun(ctx, runID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	// Recording the same fixture twice under the same run must be rejected
	// — outcomes are append-only per (run, fixture), never silently
	// overwritten.
	if err := store.RecordOutcome(ctx, runID, outcomes[0], now); err == nil {
		t.Fatal("expected duplicate outcome to be rejected")
	}

	run, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.SuiteID != "r30" || run.SubjectID != "decisiongraph" || run.CompletedAt == nil {
		t.Fatalf("run=%+v", run)
	}

	loaded, err := store.ListOutcomes(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(outcomes) {
		t.Fatalf("loaded %d outcomes, want %d", len(loaded), len(outcomes))
	}
	for _, outcome := range loaded {
		if !outcome.Passed {
			t.Fatalf("persisted outcome for %s unexpectedly failed: %v", outcome.FixtureID, outcome.ViolatedInvariants)
		}
	}

	// A run in another organization must never be readable through this
	// store — the same namespace-leakage discipline R30's hard gates
	// require of retrieval applies to evaluation results about it.
	otherStore, err := evaluationpostgres.New(platform, "other-org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherStore.GetRun(ctx, runID); err == nil {
		t.Fatal("expected cross-organization run read to fail")
	}
	if outcomes, err := otherStore.ListOutcomes(ctx, runID); err != nil || len(outcomes) != 0 {
		t.Fatalf("cross-organization outcome list=%v err=%v, want empty/no error", outcomes, err)
	}
}

func openEvaluationStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "evaluation-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetEvaluationSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset evaluation schema: %v", err)
	}
}

func syncEvaluationCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, evaluationIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !res.Applied {
		t.Fatalf("sync=%+v err=%v", res, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, evaluationIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}
