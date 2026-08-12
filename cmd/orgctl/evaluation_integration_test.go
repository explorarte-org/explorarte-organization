//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	evaluationpostgres "github.com/Mireuz13/explorarte-organization/internal/evaluation/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
)

// TestEvaluationRunReportsFullCoverageForTodaysActivatedFixtures exercises
// the real CLI path (runEvaluation, real Postgres): every bridge package
// wired in evaluationRunners now activates a real fixture, so the catalog
// is fully runner-ready (14/14) — skipped must be exactly 0, and this test
// intentionally asserts that rather than tolerating it, so a future
// regression that silently drops a runner shows up here immediately.
func TestEvaluationRunReportsFullCoverageForTodaysActivatedFixtures(t *testing.T) {
	seedEvaluationOrganization(t)
	// endtoendfixtures.Runner (r30-14) drives a real internal/executive
	// orchestration, which reads docs/canonical/capability-matrix.yaml via
	// authorizationbootstrap.Open — unlike every other bridge runner here,
	// it does not take a shortcut around config.Load()'s canonical dir
	// resolution, so this test must point it at the real repo-root
	// docs/canonical the same way seedEvaluationOrganization already does.
	t.Setenv("ORG_CANONICAL_DIR", filepath.Join("..", "..", "docs", "canonical"))
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"run", "--suite", "r30", "--mode", "r30.1-3-coverage-smoke", "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result struct {
		RunID            int64 `json:"run_id"`
		Executed         int   `json:"executed"`
		Failed           int   `json:"failed"`
		Skipped          int   `json:"skipped"`
		ExpectedReady    int   `json:"expected_ready"`
		CoverageComplete bool  `json:"coverage_complete"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode run output: %v raw=%s", err, stdout.String())
	}
	if !result.CoverageComplete {
		t.Fatalf("result=%+v want coverage_complete=true", result)
	}
	if result.Executed != result.ExpectedReady {
		t.Fatalf("result=%+v want executed == expected_ready", result)
	}
	if result.Failed != 0 {
		t.Fatalf("result=%+v want failed=0", result)
	}
	if result.Skipped != 0 {
		t.Fatalf("result=%+v want skipped=0 — all 14 R30 fixtures now have a real runner", result)
	}
	if result.ExpectedReady != 14 || result.Executed != 14 {
		t.Fatalf("result=%+v want expected_ready=14 executed=14", result)
	}

	// A run compared against itself must succeed — same, complete fixture
	// set, both sides trivially identical.
	stdout.Reset()
	stderr.Reset()
	if code := runEvaluation([]string{"compare", strconv.FormatInt(result.RunID, 10), strconv.FormatInt(result.RunID, 10)}, &stdout, &stderr); code != exitOK {
		t.Fatalf("compare run against itself: code=%d stderr=%s", code, stderr.String())
	}
}

// seedEvaluationOrganization ensures the organization (and the owner/ceo
// roles its organizations row FKs to) exists — this test file runs in
// isolation from internal/memory|rag/postgres's own canonical-registry
// sync, which is what normally creates that state for the shared test
// database. Mirrors those packages' own syncCanonical helpers exactly,
// since organizations.owner_role_id/ceo_role_id are real FKs into
// organization_roles, not columns a hand-written INSERT can satisfy
// without also creating the org's full canonical registry.
func seedEvaluationOrganization(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := platformpostgres.Open(ctx, cfg.Database, "orgctl-evaluation-test-seed")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := testdbguard.RequireTestDatabase(ctx, os.Getenv("ORG_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	if existing, err := repo.GetCurrentRevision(ctx, cfg.Tasks.OrganizationID); err == nil && existing != nil {
		return cfg
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, cfg.Tasks.OrganizationID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := service.SynchronizeCanonical(ctx, true); err != nil || !res.Applied {
		t.Fatalf("sync canonical registry: applied=%v err=%v", res.Applied, err)
	}
	return cfg
}

// openEvaluationStoreForTest mirrors what runEvaluation/evaluationCompare do
// internally (config.Load + platformpostgres.Open + evaluationpostgres.New)
// so this test can fabricate run states the CLI itself can never produce
// today (an incomplete run, two runs with different fixture sets) without
// duplicating orgctl's own machinery.
func openEvaluationStoreForTest(t *testing.T) (*evaluationpostgres.Store, string) {
	t.Helper()
	cfg := seedEvaluationOrganization(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := platformpostgres.Open(ctx, cfg.Database, "orgctl-evaluation-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := testdbguard.RequireTestDatabase(ctx, os.Getenv("ORG_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	evalStore, err := evaluationpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	return evalStore, cfg.Tasks.OrganizationID
}

// TestEvaluationCompareRejectsAnIncompleteRun proves R30.1-3's fail-closed
// contract directly against the store: a run evaluationRun would leave
// incomplete (completed_at never set, because it fell short of the
// catalog's runner-ready fixtures) must never be usable by compare — this
// is exactly the "4/14 read as full approval" risk the finding described,
// closed by refusing to compare a run nobody ever marked complete.
func TestEvaluationCompareRejectsAnIncompleteRun(t *testing.T) {
	evalStore, _ := openEvaluationStoreForTest(t)
	ctx := context.Background()
	now := time.Now().UTC()

	incompleteRunID, err := evalStore.CreateRun(ctx, "r30", "r30.1-3-incomplete-fixture", "orgctl-evaluation-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := evalStore.RecordOutcome(ctx, incompleteRunID, fixtures.RunOutcome{FixtureID: "r30-08-budget-exhaustion", Passed: true, InvariantResults: map[string]bool{"ok": true}}, now); err != nil {
		t.Fatal(err)
	}
	// Deliberately never call CompleteRun — this is the state a real
	// evaluationRun call now leaves behind whenever coverage falls short.

	completeRunID, err := evalStore.CreateRun(ctx, "r30", "r30.1-3-incomplete-fixture", "orgctl-evaluation-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := evalStore.RecordOutcome(ctx, completeRunID, fixtures.RunOutcome{FixtureID: "r30-08-budget-exhaustion", Passed: true, InvariantResults: map[string]bool{"ok": true}}, now); err != nil {
		t.Fatal(err)
	}
	if err := evalStore.CompleteRun(ctx, completeRunID, now); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"compare", strconv.FormatInt(incompleteRunID, 10), strconv.FormatInt(completeRunID, 10)}, &stdout, &stderr)
	if code != exitCompletionInconclusive {
		t.Fatalf("compare incomplete-vs-complete: code=%d want %d, stderr=%s", code, exitCompletionInconclusive, stderr.String())
	}
}

// TestEvaluationCompareRejectsMismatchedFixtureSets proves the other half
// of R30.1-3: two complete runs that nonetheless cover different fixture
// sets (e.g. the suite was extended between runs) must never be compared
// as if they were equivalent — a fixture present in one run but silently
// absent from the other must be a hard error, not a row compare quietly
// treats as "unchanged" by omission.
func TestEvaluationCompareRejectsMismatchedFixtureSets(t *testing.T) {
	evalStore, _ := openEvaluationStoreForTest(t)
	ctx := context.Background()
	now := time.Now().UTC()

	runAID, err := evalStore.CreateRun(ctx, "r30", "r30.1-3-mismatch-a", "orgctl-evaluation-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := evalStore.RecordOutcome(ctx, runAID, fixtures.RunOutcome{FixtureID: "r30-08-budget-exhaustion", Passed: true, InvariantResults: map[string]bool{"ok": true}}, now); err != nil {
		t.Fatal(err)
	}
	if err := evalStore.CompleteRun(ctx, runAID, now); err != nil {
		t.Fatal(err)
	}

	runBID, err := evalStore.CreateRun(ctx, "r30", "r30.1-3-mismatch-b", "orgctl-evaluation-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := evalStore.RecordOutcome(ctx, runBID, fixtures.RunOutcome{FixtureID: "r30-11-dag-cycles-depth-terminal-evidence", Passed: true, InvariantResults: map[string]bool{"ok": true}}, now); err != nil {
		t.Fatal(err)
	}
	if err := evalStore.CompleteRun(ctx, runBID, now); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"compare", strconv.FormatInt(runAID, 10), strconv.FormatInt(runBID, 10)}, &stdout, &stderr)
	if code != exitInvalid {
		t.Fatalf("compare mismatched fixture sets: code=%d want %d, stderr=%s", code, exitInvalid, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected an explanatory message on stderr")
	}
}
