//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	stageRole  = "ingenieria_ia/code-runner"
	reviewRole = "empresa/human"
)

func TestStagingPromotionPostgreSQL17AndRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for staging integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	store := openIntegrationStore(t, ctx)
	defer store.Close()
	resetIntegrationSchema(t, ctx, store)
	syncCanonical(t, ctx, store)

	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	baseCommit := initializeRepository(t, repositoryRoot)
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	quarantineRoot := filepath.Join(t.TempDir(), "quarantine")
	catalogFile := filepath.Join(t.TempDir(), "repositories.yaml")
	catalogBody := fmt.Sprintf("schema_version: 1\nrepositories:\n  - id: integration-repository\n    path: %s\n    enabled: true\n    allowed_target_refs:\n      - refs/heads/main\n", repositoryRoot)
	if err := os.WriteFile(catalogFile, []byte(catalogBody), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := integrationConfig(catalogFile, workspaceRoot, artifactRoot, quarantineRoot)
	runtime, err := bootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open staging runtime: %v", err)
	}
	taskService := openTaskService(t, store)

	required := true
	created, inserted, err := taskService.CreateTask(ctx, tasks.CreateRequest{
		AssignedRoleID:     stageRole,
		IdempotencyKey:     "staging-integration-1",
		Title:              "Stage a deterministic repository change",
		Instructions:       "Modify only the isolated worktree.",
		AcceptanceCriteria: []string{"candidate commit is immutable", "promotion uses compare-and-swap"},
		Requirements: []tasks.RequirementSpec{
			{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "candidate manifest and patch", Required: &required},
			{Key: "controlled-check", Type: tasks.RequirementCheck, Description: "controlled test report", Required: &required},
			{Key: "human-review", Type: tasks.RequirementApproval, Description: "explicit review", Required: &required},
		},
	}, "human", "eduardo")
	if err != nil || !inserted {
		t.Fatalf("create task: inserted=%v err=%v", inserted, err)
	}
	detail, err := taskService.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var artifactRequirement, checkRequirement, approvalRequirement int64
	for _, requirement := range detail.Requirements {
		switch requirement.Type {
		case tasks.RequirementArtifact:
			artifactRequirement = requirement.ID
		case tasks.RequirementCheck:
			checkRequirement = requirement.ID
		case tasks.RequirementApproval:
			approvalRequirement = requirement.ID
		}
	}
	if artifactRequirement == 0 || checkRequirement == 0 || approvalRequirement == 0 {
		t.Fatalf("missing requirements: %+v", detail.Requirements)
	}

	claimed, err := taskService.ClaimTasks(ctx, tasks.ClaimRequest{WorkerID: "integration-runner", BatchSize: 1, LeaseDuration: time.Minute})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim task: %+v err=%v", claimed, err)
	}
	lease := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed[0].Attempt.ID, LeaseToken: claimed[0].LeaseToken, ActorID: "integration-runner"}
	if _, err = taskService.StartAttempt(ctx, lease); err != nil {
		t.Fatal(err)
	}

	workspace, err := runtime.Service.CreateWorkspace(ctx, staging.CreateWorkspaceCommand{
		TaskID: created.ID, AttemptID: claimed[0].Attempt.ID, RepositoryID: "integration-repository",
		BaseCommit: baseCommit, TargetRef: "refs/heads/main", HolderID: "integration-runner",
		ActorRoleID: stageRole, ArtifactRequirementID: artifactRequirement, LeaseToken: claimed[0].LeaseToken,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if got := gitOutput(t, repositoryRoot, "rev-parse", "refs/heads/main"); got != baseCommit {
		t.Fatalf("target moved during workspace creation: got=%s want=%s", got, baseCommit)
	}
	workspacePath := filepath.Join(workspaceRoot, workspace.WorkspaceKey)
	if err := os.WriteFile(filepath.Join(workspacePath, "change.txt"), []byte("isolated staging change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err = runtime.Service.SealWorkspace(ctx, staging.SealWorkspaceCommand{WorkspaceID: workspace.ID, HolderID: "integration-runner", ActorRoleID: stageRole, LeaseToken: claimed[0].LeaseToken})
	if err != nil {
		t.Fatalf("seal workspace: %v", err)
	}
	if workspace.Status != staging.WorkspaceSealed || workspace.CandidateCommit == nil || workspace.ManifestDigest == nil || workspace.PatchDigest == nil {
		t.Fatalf("unexpected sealed workspace: %+v", workspace)
	}
	if got := gitOutput(t, repositoryRoot, "rev-parse", "refs/heads/main"); got != baseCommit {
		t.Fatalf("target moved during sealing: got=%s want=%s", got, baseCommit)
	}
	parent := gitOutput(t, repositoryRoot, "rev-parse", *workspace.CandidateCommit+"^")
	if parent != baseCommit {
		t.Fatalf("candidate parent=%s want=%s", parent, baseCommit)
	}

	awaiting, err := taskService.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{LeaseCommand: lease, Result: tasks.AttemptResult{Outcome: tasks.OutcomeSucceeded, Summary: "candidate sealed"}})
	if err != nil || awaiting.Status != tasks.StatusAwaitingVerification {
		t.Fatalf("record attempt result: task=%+v err=%v", awaiting, err)
	}
	promotion, err := runtime.Service.RequestPromotion(ctx, staging.RequestPromotionCommand{WorkspaceID: workspace.ID, ActorRoleID: stageRole})
	if err != nil || promotion.Status != staging.PromotionAwaitingGates {
		t.Fatalf("request promotion: %+v err=%v", promotion, err)
	}
	// Approval and checks are intentionally recorded in the less convenient order.
	// The last satisfied gate must approve the promotion regardless of arrival order.
	promotion, err = runtime.Service.SubmitReview(ctx, staging.SubmitReviewCommand{PromotionID: promotion.ID, RequirementID: approvalRequirement, Decision: staging.ReviewApprove, ActorRoleID: reviewRole, Reason: "reviewed immutable manifest before the controlled check arrived", Reference: "review://integration/approved"})
	if err != nil || promotion.Status != staging.PromotionAwaitingGates {
		t.Fatalf("early review promotion: %+v err=%v", promotion, err)
	}
	if _, err = runtime.Service.RecordCheck(ctx, staging.RecordCheckCommand{WorkspaceID: workspace.ID, RequirementID: checkRequirement, Name: "integration", Status: staging.CheckPassed, Reference: "check://integration/passed", ActorRoleID: stageRole}); err != nil {
		t.Fatalf("record check: %v", err)
	}
	promotion, err = runtime.Service.GetPromotion(ctx, promotion.ID)
	if err != nil || promotion.Status != staging.PromotionApproved {
		t.Fatalf("last gate did not approve promotion: %+v err=%v", promotion, err)
	}
	promotion, err = runtime.Service.ApplyPromotion(ctx, staging.ApplyPromotionCommand{PromotionID: promotion.ID, ActorRoleID: stageRole})
	if err != nil || promotion.Status != staging.PromotionApplied {
		t.Fatalf("apply promotion: %+v err=%v", promotion, err)
	}
	secondApply, err := runtime.Service.ApplyPromotion(ctx, staging.ApplyPromotionCommand{PromotionID: promotion.ID, ActorRoleID: stageRole})
	if err != nil || secondApply.Status != staging.PromotionApplied || secondApply.ID != promotion.ID {
		t.Fatalf("idempotent apply: %+v err=%v", secondApply, err)
	}
	if got := gitOutput(t, repositoryRoot, "rev-parse", "refs/heads/main"); got != *workspace.CandidateCommit {
		t.Fatalf("target=%s want candidate=%s", got, *workspace.CandidateCommit)
	}
	// Applying again is idempotent at the service boundary: the target already points
	// to the candidate and PostgreSQL remains applied.
	if current, getErr := runtime.Service.GetPromotion(ctx, promotion.ID); getErr != nil || current.Status != staging.PromotionApplied {
		t.Fatalf("read applied promotion: %+v err=%v", current, getErr)
	}

	workspace, err = runtime.Service.CleanupWorkspace(ctx, staging.CleanupWorkspaceCommand{WorkspaceID: workspace.ID, ActorRoleID: stageRole})
	if err != nil || workspace.Status != staging.WorkspaceCleaned {
		t.Fatalf("cleanup workspace: %+v err=%v", workspace, err)
	}
	for _, digest := range []string{*workspace.ManifestDigest, *workspace.PatchDigest} {
		if err := runtime.Service.VerifyArtifact(ctx, "artifact://sha256/"+digest); err != nil {
			t.Fatalf("artifact %s did not survive cleanup: %v", digest, err)
		}
	}
}

func openIntegrationStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("ORG_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: databaseURL, SSLMode: "disable", MaxConns: 8, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 30 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "staging-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
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
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
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

func openTaskService(t *testing.T, store *platformpostgres.Store) *tasks.Service {
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

func integrationConfig(catalogFile, workspaceRoot, artifactRoot, quarantineRoot string) config.Config {
	return config.Config{
		Registry: config.RegistryConfig{CanonicalDir: filepath.Join("..", "..", "..", "docs", "canonical"), SyncTimeout: 30 * time.Second},
		Tasks:    config.TaskConfig{OrganizationID: "explorarte", OutboxMaxAttempts: 3},
		Staging:  config.StagingConfig{Enabled: true, RepositoriesFile: catalogFile, WorkspaceRoot: workspaceRoot, ArtifactRoot: artifactRoot, QuarantineRoot: quarantineRoot, CommandTimeout: time.Minute, MaxArtifactBytes: 16 << 20, MaxChangedFiles: 100, StaleAfter: 2 * time.Minute, ReconcileInterval: time.Minute, ReconcileBatchSize: 100, GitBinary: "git"},
	}
}

func initializeRepository(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "init", "-b", "main")
	gitRun(t, root, "config", "user.name", "Integration")
	gitRun(t, root, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "--", "README.md")
	gitRun(t, root, "commit", "-m", "base")
	base := gitOutput(t, root, "rev-parse", "HEAD")
	gitRun(t, root, "checkout", "--detach", base)
	return base
}

func gitRun(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
