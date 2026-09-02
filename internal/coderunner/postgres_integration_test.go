//go:build integration

package coderunner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// TestCodeRunnerDurableTaskToEvidencePostgreSQL proves the full path this
// mission is closing end to end against a real, disposable PostgreSQL
// instance and a real git repository: durable task -> canonical-shaped
// principal claim -> StartAttempt -> staging workspace -> typed
// APPLY_PATCH/GOFMT/GO_TEST/GIT_DIFF mutation and verification -> Seal ->
// durable RecordEvidence -> RecordAttemptResult(succeeded). It then reads
// PostgreSQL back to prove the evidence row actually exists, its candidate
// identity matches what staging actually sealed, and the recorded checks
// prove verification ran after the mutation.
//
// This is the write side of "task succeeded only after evidence": that the
// worker's own control flow enforces the ordering is proven separately and
// far more cheaply by TestWorkerBlocksSuccessWhenEvidencePersistenceFails.
// This test is what proves the real durable write actually round-trips.
func TestCodeRunnerDurableTaskToEvidencePostgreSQL(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for this integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	store := openIntegrationStore(t, ctx)
	defer store.Close()
	resetIntegrationSchema(t, ctx, store)
	syncCanonical(t, ctx, store)

	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	baseCommit := initializeCodeRunnerRepository(t, repositoryRoot)
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	quarantineRoot := filepath.Join(t.TempDir(), "quarantine")
	catalogFile := filepath.Join(t.TempDir(), "repositories.yaml")
	catalogBody := "schema_version: 1\nrepositories:\n  - id: coderunner-integration-repository\n    path: " + repositoryRoot + "\n    enabled: true\n    allowed_target_refs:\n      - refs/heads/main\n"
	if err := os.WriteFile(catalogFile, []byte(catalogBody), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := codeRunnerIntegrationConfig(catalogFile, workspaceRoot, artifactRoot, quarantineRoot)
	stagingRuntime, err := bootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open staging runtime: %v", err)
	}
	taskService := openCodeRunnerTaskService(t, store)

	plan := `{"schema_version":"code-runner-execution/v1","operations":[` +
		`{"type":"APPLY_PATCH","patch":"diff --git a/value.go b/value.go\n--- a/value.go\n+++ b/value.go\n@@ -1,3 +1,3 @@\n package fixture\n \n-func Value() int { return 1 }\n+func Value() int { return 2 }\n"},` +
		`{"type":"GOFMT","path":"value.go"},` +
		`{"type":"GO_TEST","packages":["./..."]},` +
		`{"type":"GIT_DIFF"}]}`

	required := true
	created, inserted, err := taskService.CreateTask(ctx, tasks.CreateRequest{
		AssignedRoleID: coderunner.RoleID,
		IdempotencyKey: "coderunner-integration-1",
		Title:          "Apply a deterministic, verified change",
		Instructions:   plan,
		Requirements: []tasks.RequirementSpec{
			{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "candidate manifest and patch", Required: &required},
		},
	}, "human", "eduardo")
	if err != nil || !inserted {
		t.Fatalf("create task: inserted=%v err=%v", inserted, err)
	}

	executor := &coderunner.Executor{MaxOutput: 1 << 20, OperationTimeout: 30 * time.Second}
	worker := coderunner.Worker{
		Queue:    taskService,
		Executor: executor,
		Workspace: coderunner.StagingAdapter{
			Service:       stagingRuntime.Service,
			Tasks:         taskService,
			WorkspaceRoot: workspaceRoot,
			RepositoryID:  "coderunner-integration-repository",
			BaseCommit:    baseCommit,
			TargetRef:     "refs/heads/main",
		},
		WorkerID:          "integration-runner",
		HolderPrincipalID: "integration-runner",
		LeaseDuration:     time.Minute,
		RuntimeVersion:    "integration-test",
	}
	n, err := worker.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce: n=%d err=%v", n, err)
	}

	detail, err := taskService.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attempts) != 1 {
		t.Fatalf("expected exactly one attempt, got %+v", detail.Attempts)
	}
	attempt := detail.Attempts[0]
	if attempt.FailureCode != nil && *attempt.FailureCode != "" {
		summary := ""
		if attempt.ResultSummary != nil {
			summary = *attempt.ResultSummary
		}
		t.Fatalf("attempt failed: code=%s summary=%s", *attempt.FailureCode, summary)
	}
	if attempt.ResultSummary == nil || !strings.Contains(*attempt.ResultSummary, "succeeded") {
		t.Fatalf("unexpected result summary: %v", attempt.ResultSummary)
	}

	// Staging's own SealWorkspace already records an "artifact" evidence row
	// for the manifest; CodeRunner's attempt evidence is the separate
	// "result" row this mission adds. Find it explicitly rather than
	// assuming CodeRunner is the only evidence producer for this task.
	var ev *tasks.Evidence
	for i := range detail.Evidence {
		if detail.Evidence[i].Type == tasks.RequirementResult {
			ev = &detail.Evidence[i]
		}
	}
	if ev == nil {
		t.Fatalf("no code-runner attempt evidence (type=result) found among %d evidence rows: %+v", len(detail.Evidence), detail.Evidence)
	}
	if ev.Digest == nil || len(*ev.Digest) != 64 {
		t.Fatalf("evidence missing a SHA-256 digest: %+v", ev)
	}
	if ev.Metadata == nil {
		t.Fatal("evidence missing structured metadata")
	}

	operations, _ := ev.Metadata["operations_executed"].([]any)
	if len(operations) != 4 {
		t.Fatalf("expected 4 operations_executed, got %d: %+v", len(operations), ev.Metadata["operations_executed"])
	}
	checks, _ := ev.Metadata["checks_run"].([]any)
	if len(checks) != 1 {
		t.Fatalf("expected 1 checks_run entry (GO_TEST), got %d: %+v", len(checks), ev.Metadata["checks_run"])
	}
	if check, ok := checks[0].(map[string]any); !ok || check["type"] != "GO_TEST" || check["success"] != true {
		t.Fatalf("unexpected check entry: %+v", checks[0])
	}

	candidate, ok := ev.Metadata["candidate_revision"].(map[string]any)
	if !ok {
		t.Fatalf("evidence missing candidate_revision: %+v", ev.Metadata)
	}
	candidateCommit, _ := candidate["candidate_commit"].(string)
	if candidateCommit == "" {
		t.Fatalf("evidence candidate_revision missing candidate_commit: %+v", candidate)
	}
	// Prove the evidence's candidate identity matches a real object staging
	// actually sealed in the repository, parented on the base commit: this
	// is the seal/evidence linkage the mission requires, checked against
	// git directly rather than trusted blindly from the evidence payload.
	if kind := gitOutputCR(t, repositoryRoot, "cat-file", "-t", candidateCommit); kind != "commit" {
		t.Fatalf("evidence candidate_commit %s is not a real commit object (got %q)", candidateCommit, kind)
	}
	if parent := gitOutputCR(t, repositoryRoot, "rev-parse", candidateCommit+"^"); parent != baseCommit {
		t.Fatalf("candidate parent=%s want base=%s", parent, baseCommit)
	}
	if got := gitOutputCR(t, repositoryRoot, "rev-parse", "refs/heads/main"); got != baseCommit {
		t.Fatalf("target ref moved during staging (should still be untouched): got=%s want=%s", got, baseCommit)
	}

	digests, ok := ev.Metadata["artifact_digests"].(map[string]any)
	if !ok || digests["manifest_digest"] == "" || digests["manifest_digest"] == nil {
		t.Fatalf("evidence missing artifact_digests.manifest_digest: %+v", ev.Metadata)
	}

	env, ok := ev.Metadata["environment"].(map[string]any)
	if !ok || env["go_version"] == "" || env["git_version"] == "" {
		t.Fatalf("evidence missing execution environment: %+v", ev.Metadata)
	}
}

// TestCodeRunnerTaskClaimIsRoleScopedPostgreSQL (AUTH-005, CodeRunner stage):
// proves, against a real database rather than an in-process fake, that a
// task assigned to ingenieria_ia/code-runner cannot be claimed by a worker
// asking for a different role -- the same WHERE assigned_role_id=$N clause
// (internal/tasks/postgres/queue.go) every other role-bound worker in this
// system already relies on, exercised here for CodeRunner specifically,
// which until this test only had this property proven with fakes
// (worker_adversarial_test.go). ORG-AUDIT-010 left AssignedRoleID optional
// at the Service.ClaimTasks layer (omitting it claims from any role) --
// irrelevant here, since coderunner.Worker.RunOnce always hardcodes
// coderunner.RoleID as a Go constant, never task- or caller-supplied; this
// test's own wrong-role claim attempt supplies a role explicitly, the same
// shape a real second worker type would use.
func TestCodeRunnerTaskClaimIsRoleScopedPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	store := openIntegrationStore(t, ctx)
	defer store.Close()
	resetIntegrationSchema(t, ctx, store)
	syncCanonical(t, ctx, store)

	taskService := openCodeRunnerTaskService(t, store)

	required := true
	created, inserted, err := taskService.CreateTask(ctx, tasks.CreateRequest{
		AssignedRoleID: coderunner.RoleID,
		IdempotencyKey: "coderunner-role-scope-1",
		Title:          "Role-scoped claim probe",
		Instructions:   `{"schema_version":"code-runner-execution/v1","operations":[{"type":"GIT_STATUS"}]}`,
		Requirements: []tasks.RequirementSpec{
			{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "candidate manifest and patch", Required: &required},
		},
	}, "human", "eduardo")
	if err != nil || !inserted {
		t.Fatalf("create task: inserted=%v err=%v", inserted, err)
	}

	// A worker asking for a DIFFERENT role must see nothing: the real SQL
	// WHERE assigned_role_id=$N clause, not an application-level filter
	// that a caller could bypass.
	wrongRoleClaims, err := taskService.ClaimTasks(ctx, tasks.ClaimRequest{
		WorkerID: "impostor-worker", HolderPrincipalID: "impostor-worker",
		AssignedRoleID: "ingenieria_ia/orquestador", BatchSize: 10, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("wrong-role claim: %v", err)
	}
	for _, claim := range wrongRoleClaims {
		if claim.Task.ID == created.ID {
			t.Fatalf("task %d assigned to %s was claimable under role ingenieria_ia/orquestador -- role isolation is broken", created.ID, coderunner.RoleID)
		}
	}

	// The correctly role-bound worker must still be able to claim it.
	correctClaims, err := taskService.ClaimTasks(ctx, tasks.ClaimRequest{
		WorkerID: "real-code-runner", HolderPrincipalID: "real-code-runner",
		AssignedRoleID: coderunner.RoleID, BatchSize: 10, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("correct-role claim: %v", err)
	}
	found := false
	for _, claim := range correctClaims {
		if claim.Task.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("task %d was not claimable by its own assigned role %s", created.ID, coderunner.RoleID)
	}
}

func initializeCodeRunnerRepository(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunCR(t, root, "init", "-b", "main")
	gitRunCR(t, root, "config", "user.name", "Integration")
	gitRunCR(t, root, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module coderunnerfixture\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "value.go"), []byte("package fixture\n\nfunc Value() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunCR(t, root, "add", "--", "go.mod", "value.go")
	gitRunCR(t, root, "commit", "-m", "base")
	base := gitOutputCR(t, root, "rev-parse", "HEAD")
	gitRunCR(t, root, "checkout", "--detach", base)
	return base
}

func gitRunCR(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutputCR(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	out, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func openIntegrationStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("ORG_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: databaseURL, SSLMode: "disable", MaxConns: 8, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 30 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "coderunner-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func resetIntegrationSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
		TRUNCATE staging_events,staging_reviews,staging_promotions,staging_checks,
		         staging_workspace_artifacts,staging_artifacts,staging_workspaces,
		         outbox_events,task_dead_letters,task_events,task_leases,task_attempts,
		         task_evidence,task_requirements,task_dependencies,tasks,
		         organization_reporting_lines,organization_registry_revision_documents,
		         organization_roles,organizational_units,organizations,
		         organization_registry_revisions,audit_events RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatal(err)
	}
}

func syncCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repository, "explorarte", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := service.SynchronizeCanonical(ctx, true); err != nil || !result.Applied {
		t.Fatalf("sync canonical: result=%+v err=%v", result, err)
	}
}

func openCodeRunnerTaskService(t *testing.T, store *platformpostgres.Store) *tasks.Service {
	t.Helper()
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := registryadapter.New(repository)
	if err != nil {
		t.Fatal(err)
	}
	database, err := taskpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := tasks.NewService(database, catalog, tasks.Config{OrganizationID: "explorarte", DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 15 * time.Minute, RetryPolicy: tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 3, OutboxClaimDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func codeRunnerIntegrationConfig(catalogFile, workspaceRoot, artifactRoot, quarantineRoot string) config.Config {
	return config.Config{
		Registry: config.RegistryConfig{CanonicalDir: filepath.Join("..", "..", "docs", "canonical"), SyncTimeout: 30 * time.Second},
		Tasks:    config.TaskConfig{OrganizationID: "explorarte", OutboxMaxAttempts: 3},
		Staging:  config.StagingConfig{Enabled: true, RepositoriesFile: catalogFile, WorkspaceRoot: workspaceRoot, ArtifactRoot: artifactRoot, QuarantineRoot: quarantineRoot, CommandTimeout: time.Minute, MaxArtifactBytes: 16 << 20, MaxChangedFiles: 100, StaleAfter: 2 * time.Minute, ReconcileInterval: time.Minute, ReconcileBatchSize: 100, GitBinary: "git"},
	}
}
