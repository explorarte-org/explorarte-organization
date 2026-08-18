//go:build integration

package engineeringmission_test

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
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
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
	// department_leadership authority class carries task.review
	// (capability-matrix.yaml) and is an enabled (status: imported_source)
	// role, distinct from ingenieria_ia/code-runner (execution_service),
	// making it a valid independent reviewer for this mission's author.
	reviewerRole = "ingenieria_ia/orquestador"
	repositoryID = "engineeringmission-integration-repository"
	targetRef    = "refs/heads/main"
)

// TestEngineeringMissionApprovedNotAppliedPostgreSQL is the bootstrap's
// principal acceptance test (B6-B9): a harmless engineering mission runs
// the full governed path -- durable MissionPolicy -> BaseSHA-bound isolated
// workspace -> bounded CodeRunner mutation -> real gates -> sealed
// CandidateSHA -> durable evidence -> RequestPromotion -> independent
// reviewer (a different role than the workspace author, never the
// author's own role even when that role happens to carry owner authority)
// -> SubmitReview(APPROVE) -> PromotionApproved -- and proves the target
// ref never moved and ApplyPromotion was never reachable/called.
func TestEngineeringMissionApprovedNotAppliedPostgreSQL(t *testing.T) {
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
	baseSHA := initializeMissionRepository(t, repositoryRoot)
	stagingRuntime, taskService, workspaceRoot := openMissionRuntime(t, ctx, store, repositoryRoot)

	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}

	policy := engineeringmission.MissionPolicy{
		BaseSHA:            baseSHA,
		Objective:          "bump the fixture value inside the allowed boundary",
		AllowedPaths:       []string{"fixture"},
		AcceptanceCriteria: []string{"fixture tests pass"},
		RequiredGates: []engineeringmission.RequiredGate{
			{Type: engineeringmission.GateBuild, Packages: []string{"./..."}},
			{Type: engineeringmission.GateTest, Packages: []string{"./..."}},
		},
	}
	plan := missionPlan(`diff --git a/fixture/value.go b/fixture/value.go
--- a/fixture/value.go
+++ b/fixture/value.go
@@ -1,3 +1,3 @@
 package fixture

-func Value() int { return 1 }
+func Value() int { return 2 }
`, "fixture/value.go")

	task, err := mission.Create(ctx, policy, plan, "explorarte", reviewerRole, "human", "eduardo")
	if err != nil {
		t.Fatalf("Create mission: %v", err)
	}

	resolvedPolicy, err := policy.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	worker := coderunner.Worker{
		Queue:    taskService,
		Executor: &coderunner.Executor{MaxOutput: 1 << 20, OperationTimeout: 30 * time.Second},
		Workspace: coderunner.StagingAdapter{
			Service:       stagingRuntime.Service,
			Tasks:         taskService,
			WorkspaceRoot: workspaceRoot,
			IntentResolver: engineeringmission.WorkspaceResolver{
				Tasks:        taskService,
				Mission:      mission,
				RepositoryID: repositoryID,
				TargetRef:    targetRef,
			},
		},
		PlanGuard:         engineeringmission.Guard{Policy: resolvedPolicy},
		WorkerID:          "engineeringmission-integration-runner",
		HolderPrincipalID: coderunner.RoleID,
		LeaseDuration:     time.Minute,
		RuntimeVersion:    "integration-test",
	}
	n, err := worker.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce: n=%d err=%v", n, err)
	}

	detail, err := taskService.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attempts) != 1 || detail.Attempts[0].FailureCode != nil {
		t.Fatalf("expected one successful attempt, got %+v", detail.Attempts)
	}

	var workspaceID int64
	for _, ev := range detail.Evidence {
		if strings.HasPrefix(ev.Reference, "code-runner-attempt-evidence://") {
			if cand, ok := ev.Metadata["candidate_revision"].(map[string]any); ok {
				if id, ok := cand["workspace_id"].(float64); ok {
					workspaceID = int64(id)
				}
			}
		}
	}
	if workspaceID == 0 {
		t.Fatalf("code-runner attempt evidence missing candidate_revision.workspace_id: %+v", detail.Evidence)
	}

	workspace, err := stagingRuntime.Service.GetWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Status != staging.WorkspaceSealed || workspace.CandidateCommit == nil {
		t.Fatalf("expected a sealed workspace with a candidate commit: %+v", workspace)
	}
	candidateSHA := *workspace.CandidateCommit
	if candidateSHA == baseSHA {
		t.Fatalf("candidate SHA must differ from base SHA, got %s for both", baseSHA)
	}
	if kind := gitOutputEM(t, repositoryRoot, "cat-file", "-t", candidateSHA); kind != "commit" {
		t.Fatalf("candidate SHA %s is not a real commit object (got %q)", candidateSHA, kind)
	}

	promotion, err := mission.RequestPromotion(ctx, task.ID, workspaceID, coderunner.RoleID)
	if err != nil {
		t.Fatalf("RequestPromotion: %v", err)
	}
	if promotion.Status != staging.PromotionAwaitingGates {
		t.Fatalf("promotion status = %q, want %q", promotion.Status, staging.PromotionAwaitingGates)
	}

	var approvalRequirementID int64
	for _, r := range detail.Requirements {
		if r.Key == "review" {
			approvalRequirementID = r.ID
		}
	}
	if approvalRequirementID == 0 {
		t.Fatal("review requirement not found on task")
	}

	if reviewerRole == workspace.ActorRoleID {
		t.Fatalf("test fixture bug: reviewer role %q must differ from workspace author role %q", reviewerRole, workspace.ActorRoleID)
	}
	approved, err := mission.ReviewMission(ctx, promotion.ID, approvalRequirementID, reviewerRole, engineeringmission.Approve, "looks_good", "fixture change is safe and gated")
	if err != nil {
		t.Fatalf("ReviewMission(APPROVE): %v", err)
	}
	if approved.Status != staging.PromotionApproved {
		t.Fatalf("promotion status after approval = %q, want %q", approved.Status, staging.PromotionApproved)
	}

	// PROMOTION BOUNDARY: the target ref must be byte-identical to what it
	// was before the mission ever ran, and no code path in this test (or in
	// engineeringmission.PromotionPort, which structurally excludes
	// ApplyPromotion/PromoteRef) ever asked staging to apply the promotion.
	if got := gitOutputEM(t, repositoryRoot, "rev-parse", targetRef); got != baseSHA {
		t.Fatalf("target ref moved: before=%s after=%s -- ApplyPromotion must never be reachable from this path", baseSHA, got)
	}
}

// TestEngineeringMissionDeniesOutsideAllowedPaths is negative A: a patch
// touching a path outside MissionPolicy.AllowedPaths must be denied before
// mutation ever reaches the repository -- Worker.PlanGuard.ValidatePlan
// runs before Execute, so the attempt must fail closed with no successful
// candidate and the target ref untouched.
func TestEngineeringMissionDeniesOutsideAllowedPaths(t *testing.T) {
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
	baseSHA := initializeMissionRepository(t, repositoryRoot)
	stagingRuntime, taskService, workspaceRoot := openMissionRuntime(t, ctx, store, repositoryRoot)
	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}

	policy := engineeringmission.MissionPolicy{
		BaseSHA:            baseSHA,
		Objective:          "attempt to escape the allowed boundary",
		AllowedPaths:       []string{"fixture"},
		AcceptanceCriteria: []string{"n/a"},
		RequiredGates:      []engineeringmission.RequiredGate{{Type: engineeringmission.GateTest, Packages: []string{"./..."}}},
	}
	// The patch touches outside/other.go, which is NOT under AllowedPaths.
	plan := missionPlan(`diff --git a/outside/other.go b/outside/other.go
--- a/outside/other.go
+++ b/outside/other.go
@@ -1,3 +1,3 @@
 package outside

-func Other() int { return 1 }
+func Other() int { return 999 }
`, "outside/other.go")

	task, err := mission.Create(ctx, policy, plan, "explorarte", reviewerRole, "human", "eduardo")
	if err != nil {
		t.Fatalf("Create mission: %v", err)
	}
	resolvedPolicy, err := policy.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	worker := coderunner.Worker{
		Queue:    taskService,
		Executor: &coderunner.Executor{MaxOutput: 1 << 20, OperationTimeout: 30 * time.Second},
		Workspace: coderunner.StagingAdapter{
			Service:        stagingRuntime.Service,
			Tasks:          taskService,
			WorkspaceRoot:  workspaceRoot,
			IntentResolver: engineeringmission.WorkspaceResolver{Tasks: taskService, Mission: mission, RepositoryID: repositoryID, TargetRef: targetRef},
		},
		PlanGuard:         engineeringmission.Guard{Policy: resolvedPolicy},
		WorkerID:          "engineeringmission-integration-runner",
		HolderPrincipalID: coderunner.RoleID,
		LeaseDuration:     time.Minute,
		RuntimeVersion:    "integration-test",
	}
	n, err := worker.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce: n=%d err=%v", n, err)
	}

	detail, err := taskService.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attempts) != 1 || detail.Attempts[0].FailureCode == nil || *detail.Attempts[0].FailureCode != "mission_policy_denied" {
		t.Fatalf("expected a mission_policy_denied attempt failure, got %+v", detail.Attempts)
	}
	if got := gitOutputEM(t, repositoryRoot, "rev-parse", targetRef); got != baseSHA {
		t.Fatalf("target ref moved despite denied mutation: before=%s after=%s", baseSHA, got)
	}
}

// TestEngineeringMissionDeniesBaseSHADrift is negative B: a MissionPolicy
// whose BaseSHA no longer matches the trusted target ref's real HEAD must
// be denied at workspace-creation time (staging's own ErrTargetMoved
// check), before any mutation is attempted.
func TestEngineeringMissionDeniesBaseSHADrift(t *testing.T) {
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
	staleSHA := initializeMissionRepository(t, repositoryRoot)
	// initializeMissionRepository leaves HEAD detached at staleSHA (so
	// CodeRunner's own workspace creation is never itself on the branch
	// being mutated) -- committing while detached would NOT move
	// refs/heads/main at all. Check the branch out first so this commit is
	// a real advance of the target ref, then detach again afterward, same
	// as initializeMissionRepository's own convention.
	gitRunEM(t, repositoryRoot, "checkout", "main")
	if err := os.WriteFile(filepath.Join(repositoryRoot, "fixture", "value.go"), []byte("package fixture\n\nfunc Value() int { return 7 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunEM(t, repositoryRoot, "add", "--", "fixture/value.go")
	gitRunEM(t, repositoryRoot, "commit", "-m", "advance target ref past the mission's stale BaseSHA")
	currentSHA := gitOutputEM(t, repositoryRoot, "rev-parse", targetRef)
	if currentSHA == staleSHA {
		t.Fatal("test fixture bug: target ref did not actually advance")
	}
	gitRunEM(t, repositoryRoot, "checkout", "--detach", currentSHA)

	stagingRuntime, taskService, workspaceRoot := openMissionRuntime(t, ctx, store, repositoryRoot)
	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}

	policy := engineeringmission.MissionPolicy{
		BaseSHA:            staleSHA, // deliberately stale
		Objective:          "mission bound to a base that has since drifted",
		AllowedPaths:       []string{"fixture"},
		AcceptanceCriteria: []string{"n/a"},
		RequiredGates:      []engineeringmission.RequiredGate{{Type: engineeringmission.GateTest, Packages: []string{"./..."}}},
	}
	plan := missionPlan(`diff --git a/fixture/value.go b/fixture/value.go
--- a/fixture/value.go
+++ b/fixture/value.go
@@ -1,3 +1,3 @@
 package fixture

-func Value() int { return 7 }
+func Value() int { return 8 }
`, "fixture/value.go")

	if _, err := mission.Create(ctx, policy, plan, "explorarte", reviewerRole, "human", "eduardo"); err != nil {
		t.Fatalf("Create mission: %v", err)
	}
	resolvedPolicy, err := policy.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	worker := coderunner.Worker{
		Queue:    taskService,
		Executor: &coderunner.Executor{MaxOutput: 1 << 20, OperationTimeout: 30 * time.Second},
		Workspace: coderunner.StagingAdapter{
			Service:        stagingRuntime.Service,
			Tasks:          taskService,
			WorkspaceRoot:  workspaceRoot,
			IntentResolver: engineeringmission.WorkspaceResolver{Tasks: taskService, Mission: mission, RepositoryID: repositoryID, TargetRef: targetRef},
		},
		PlanGuard:         engineeringmission.Guard{Policy: resolvedPolicy},
		WorkerID:          "engineeringmission-integration-runner",
		HolderPrincipalID: coderunner.RoleID,
		LeaseDuration:     time.Minute,
		RuntimeVersion:    "integration-test",
	}
	// Unlike a PlanGuard denial (caught internally and recorded as an
	// attempt failure, RunOnce returning err=nil), a drifted BaseSHA fails
	// inside staging.CreateWorkspace itself -- earlier in the worker's flow,
	// before there is an attempt result to record against -- so RunOnce
	// itself returns the error directly. Either way, the workspace/target
	// ref must never be touched.
	if _, err := worker.RunOnce(ctx); err == nil || !strings.Contains(err.Error(), "target ref moved") {
		t.Fatalf("RunOnce: want an error containing \"target ref moved\" for a drifted BaseSHA, got %v", err)
	}
	if got := gitOutputEM(t, repositoryRoot, "rev-parse", targetRef); got != currentSHA {
		t.Fatalf("target ref moved during a denied drifted-base workspace creation: before=%s after=%s", currentSHA, got)
	}
}

// TestEngineeringMissionDeniesMissingRequiredGate is negative C: a mission
// whose RequiredGates demand a gate type CodeRunner's plan never actually
// ran must never reach RecordCheck/RequestPromotion -- verified directly
// against real durable task evidence (not a fake), against a real sealed
// workspace.
func TestEngineeringMissionDeniesMissingRequiredGate(t *testing.T) {
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
	baseSHA := initializeMissionRepository(t, repositoryRoot)
	stagingRuntime, taskService, workspaceRoot := openMissionRuntime(t, ctx, store, repositoryRoot)
	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}

	// RequiredGates demands GO_VET, but the plan below only runs GO_TEST --
	// VerifyRequiredGates must catch the gap even though the attempt itself
	// otherwise succeeds and seals a real candidate.
	policy := engineeringmission.MissionPolicy{
		BaseSHA:            baseSHA,
		Objective:          "mission requiring a gate the plan never runs",
		AllowedPaths:       []string{"fixture"},
		AcceptanceCriteria: []string{"n/a"},
		RequiredGates:      []engineeringmission.RequiredGate{{Type: engineeringmission.GateVet, Packages: []string{"./..."}}},
	}
	plan := missionPlan(`diff --git a/fixture/value.go b/fixture/value.go
--- a/fixture/value.go
+++ b/fixture/value.go
@@ -1,3 +1,3 @@
 package fixture

-func Value() int { return 1 }
+func Value() int { return 3 }
`, "fixture/value.go")
	// missionPlan always appends GO_TEST; this plan never runs GO_VET.

	task, err := mission.Create(ctx, policy, plan, "explorarte", reviewerRole, "human", "eduardo")
	if err != nil {
		t.Fatalf("Create mission: %v", err)
	}
	resolvedPolicy, err := policy.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	worker := coderunner.Worker{
		Queue:    taskService,
		Executor: &coderunner.Executor{MaxOutput: 1 << 20, OperationTimeout: 30 * time.Second},
		Workspace: coderunner.StagingAdapter{
			Service:        stagingRuntime.Service,
			Tasks:          taskService,
			WorkspaceRoot:  workspaceRoot,
			IntentResolver: engineeringmission.WorkspaceResolver{Tasks: taskService, Mission: mission, RepositoryID: repositoryID, TargetRef: targetRef},
		},
		PlanGuard:         engineeringmission.Guard{Policy: resolvedPolicy},
		WorkerID:          "engineeringmission-integration-runner",
		HolderPrincipalID: coderunner.RoleID,
		LeaseDuration:     time.Minute,
		RuntimeVersion:    "integration-test",
	}
	n, err := worker.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce: n=%d err=%v", n, err)
	}
	detail, err := taskService.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attempts) != 1 || detail.Attempts[0].FailureCode != nil {
		t.Fatalf("expected the CodeRunner attempt itself to succeed (the gap is a missing gate, not an execution failure): %+v", detail.Attempts)
	}

	if err := mission.VerifyRequiredGates(ctx, task.ID, resolvedPolicy); err == nil {
		t.Fatal("VerifyRequiredGates succeeded despite a required GO_VET gate that never ran")
	}

	list, err := stagingRuntime.Service.ListWorkspaces(ctx, staging.WorkspaceFilter{Status: staging.WorkspaceSealed, Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("resolve sealed workspace: list=%v err=%v", list, err)
	}
	if _, err := mission.RequestPromotion(ctx, task.ID, list[0].ID, coderunner.RoleID); err == nil {
		t.Fatal("RequestPromotion succeeded despite a required gate that never ran -- no promotion should ever be requested")
	}
	// The schema was freshly truncated at the start of this test, and
	// RequestPromotion above must have failed before ever inserting a row --
	// so an unfiltered listing proves no promotion exists at all, not just
	// none for this task.
	promotions, err := stagingRuntime.Service.ListPromotions(ctx, staging.PromotionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(promotions) != 0 {
		t.Fatalf("expected zero promotions for a task that never satisfied its required gates, got %d", len(promotions))
	}
}

// missionPlan builds the exact plan shape the mission brief specifies:
// "aplicar cambio permitido; GOFMT si corresponde; GO_BUILD; GO_TEST" --
// deliberately never GO_VET, so TestEngineeringMissionDeniesMissingRequiredGate
// (which requires a GateVet the plan never runs) stays a true negative
// regardless of which other tests share this helper.
func missionPlan(patch, gofmtPath string) string {
	escaped := strings.ReplaceAll(patch, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	return `{"schema_version":"code-runner-execution/v1","operations":[` +
		`{"type":"APPLY_PATCH","patch":"` + escaped + `"},` +
		`{"type":"GOFMT","path":"` + gofmtPath + `"},` +
		`{"type":"GO_BUILD","packages":["./..."]},` +
		`{"type":"GO_TEST","packages":["./..."]}]}`
}

func initializeMissionRepository(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "outside"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunEM(t, root, "init", "-b", "main")
	gitRunEM(t, root, "config", "user.name", "Integration")
	gitRunEM(t, root, "config", "user.email", "integration@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module engineeringmissionfixture\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture", "value.go"), []byte("package fixture\n\nfunc Value() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside", "other.go"), []byte("package outside\n\nfunc Other() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunEM(t, root, "add", "--", "go.mod", "fixture/value.go", "outside/other.go")
	gitRunEM(t, root, "commit", "-m", "base")
	base := gitOutputEM(t, root, "rev-parse", "HEAD")
	gitRunEM(t, root, "checkout", "--detach", base)
	return base
}

func openMissionRuntime(t *testing.T, ctx context.Context, store *platformpostgres.Store, repositoryRoot string) (*bootstrap.Runtime, *tasks.Service, string) {
	t.Helper()
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	quarantineRoot := filepath.Join(t.TempDir(), "quarantine")
	catalogFile := filepath.Join(t.TempDir(), "repositories.yaml")
	catalogBody := "schema_version: 1\nrepositories:\n  - id: " + repositoryID + "\n    path: " + repositoryRoot + "\n    enabled: true\n    allowed_target_refs:\n      - " + targetRef + "\n"
	if err := os.WriteFile(catalogFile, []byte(catalogBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Registry: config.RegistryConfig{CanonicalDir: filepath.Join("..", "..", "docs", "canonical"), SyncTimeout: 30 * time.Second},
		Tasks:    config.TaskConfig{OrganizationID: "explorarte", OutboxMaxAttempts: 3},
		Staging:  config.StagingConfig{Enabled: true, RepositoriesFile: catalogFile, WorkspaceRoot: workspaceRoot, ArtifactRoot: artifactRoot, QuarantineRoot: quarantineRoot, CommandTimeout: time.Minute, MaxArtifactBytes: 16 << 20, MaxChangedFiles: 100, StaleAfter: 2 * time.Minute, ReconcileInterval: time.Minute, ReconcileBatchSize: 100, GitBinary: "git"},
	}
	stagingRuntime, err := bootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open staging runtime: %v", err)
	}
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
	taskService, err := tasks.NewService(database, catalog, tasks.Config{OrganizationID: "explorarte", DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 15 * time.Minute, RetryPolicy: tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 3, OutboxClaimDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return stagingRuntime, taskService, workspaceRoot
}

func gitRunEM(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutputEM(t *testing.T, directory string, args ...string) string {
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
	store, err := platformpostgres.Open(ctx, cfg, "engineeringmission-integration")
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
