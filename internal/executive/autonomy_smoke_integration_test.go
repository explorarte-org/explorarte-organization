//go:build integration

package executive_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/testsupport"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	stagingbootstrap "github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const (
	autonomyRepositoryID = "executive-autonomy-smoke-repository"
	autonomyTargetRef    = "refs/heads/main"
	autonomyChangePath   = "internal/autonomy_smoke/value.go"
	autonomyReviewerRole = "ingenieria_ia/orquestador"
)

type autonomySnapshotSources struct{}

func (autonomySnapshotSources) SnapshotSources(context.Context, int64) ([]executive.SnapshotSource, error) {
	return nil, nil
}

// autonomyMissionProvisioner is deliberately the same narrow bridge the
// production bootstrap uses: Executive can create a mission, but it cannot
// execute, review, approve or apply it.
type autonomyMissionProvisioner struct {
	mission engineeringmission.Service
}

func (p autonomyMissionProvisioner) ProvisionMission(ctx context.Context, command executive.MissionProvisionCommand) (executive.MissionRecord, error) {
	task, err := p.mission.CreateIn(ctx, command.Policy, string(command.PlanJSON), engineeringmission.MissionOrigin{
		OrganizationID:    "explorarte",
		RequestedByRoleID: command.RequestedByRoleID,
		CorrelationID:     command.CorrelationID,
		CausationID:       command.CausationID,
	}, command.ActorType, command.ActorID)
	if err != nil {
		return executive.MissionRecord{}, err
	}
	return executive.MissionRecord{TaskID: task.ID}, nil
}

type autonomyProgramTarget struct {
	runtime    *stagingbootstrap.Runtime
	repository staging.RepositoryConfig
	targetRef  string
}

func (r autonomyProgramTarget) ResolveProgramTargetSHA(ctx context.Context) (string, error) {
	return r.runtime.Git.ReadTarget(ctx, r.repository, r.targetRef)
}

func autonomyImplementationPlan(t *testing.T) json.RawMessage {
	t.Helper()
	patch := `diff --git a/internal/autonomy_smoke/value.go b/internal/autonomy_smoke/value.go
--- a/internal/autonomy_smoke/value.go
+++ b/internal/autonomy_smoke/value.go
@@ -3 +3 @@
-func Value() string { return "before" }
+func Value() string { return "after" }
`
	body, err := json.Marshal(executive.ImplementationPlan{
		SchemaVersion: executive.ImplementationPlanSchemaVersion,
		Objective:     "apply the bounded autonomous smoke change",
		Changes: []executive.PlannedChange{{
			Path:   autonomyChangePath,
			Intent: "change the disposable fixture value",
			Patch:  patch,
		}},
		VerificationExpectations: []string{"build, vet, test and governance fitness pass"},
		DependencyOrder:          []string{},
		EvidenceRefs:             []string{},
	})
	if err != nil {
		t.Fatalf("marshal implementation plan: %v", err)
	}
	return body
}

func autonomyDesignAdjudication(ctx context.Context, h *integrationHarness, taskID int64) json.RawMessage {
	detail, err := h.tasks.GetTask(ctx, taskID)
	if err != nil {
		return nil
	}
	start := strings.Index(detail.Task.Instructions, "{")
	if start < 0 {
		return nil
	}
	var bundle struct {
		Design struct {
			ID      string `json:"design_id"`
			Version string `json:"design_version"`
			Digest  string `json:"design_digest"`
		} `json:"design"`
	}
	if err := json.NewDecoder(strings.NewReader(detail.Task.Instructions[start:])).Decode(&bundle); err != nil {
		return nil
	}
	body, err := json.Marshal(struct {
		SchemaVersion          string   `json:"schema_version"`
		Verdict                string   `json:"verdict"`
		AcceptedFindings       []string `json:"accepted_findings"`
		RejectedFindings       []string `json:"rejected_findings"`
		RequiredChanges        []string `json:"required_changes"`
		UnresolvedOwnerChoices []string `json:"unresolved_owner_decisions"`
		DesignID               string   `json:"design_id"`
		DesignVersion          string   `json:"design_version"`
		DesignDigest           string   `json:"design_digest"`
		EvidenceRefs           []string `json:"evidence_refs"`
	}{
		SchemaVersion:          "design-adjudication/v1",
		Verdict:                "freeze",
		AcceptedFindings:       []string{},
		RejectedFindings:       []string{},
		RequiredChanges:        []string{},
		UnresolvedOwnerChoices: []string{},
		DesignID:               bundle.Design.ID,
		DesignVersion:          bundle.Design.Version,
		DesignDigest:           bundle.Design.Digest,
		EvidenceRefs:           []string{},
	})
	if err != nil {
		return nil
	}
	return body
}

// TestExecutiveAutonomousDesignImplementationReviewCodeRunnerPostgreSQL is
// the local, non-provider smoke for the complete governed seam. The
// cognitive responses are scripted to make the test deterministic; everything
// after the model boundary is real: PostgreSQL tasks, design freeze, mission
// provisioning, isolated staging workspace, actual CodeRunner operations,
// all four host gates, promotion review and Executive closure.
//
// The target ref is intentionally never promoted. This proves autonomous
// design/implementation/review evidence without giving the Executive a path
// to mutate the authoritative repository.
func TestExecutiveAutonomousDesignImplementationReviewCodeRunnerPostgreSQL(t *testing.T) {
	if err := testsupport.LookPath("git"); err != nil {
		t.Skip("git is required for this integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h := newIntegrationHarness(t)
	defer h.close()

	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	baseSHA := initializeAutonomyRepository(t, repositoryRoot)
	stagingRuntime, workspaceRoot, repository := openAutonomyStagingRuntime(t, h.store, repositoryRoot)
	mission := engineeringmission.Service{Tasks: h.tasks, Promotion: stagingRuntime.Service}

	models := newIntegrationModelRuntime()
	models.implementationPlanBody = autonomyImplementationPlan(t)
	models.designAdjudicationBody = func(taskID int64) json.RawMessage {
		return autonomyDesignAdjudication(ctx, h, taskID)
	}
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate,
		executive.WithSnapshotSources(autonomySnapshotSources{}),
		executive.WithMissionProvisioning(autonomyProgramTarget{runtime: stagingRuntime, repository: repository, targetRef: autonomyTargetRef}, autonomyMissionProvisioner{mission: mission}),
	)

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID:    executive.OwnerRoleID,
		IdempotencyKey: "integration-autonomous-design-implementation-review",
		Goal: executive.OwnerGoal{
			Goal: "Design, implement and independently review one bounded disposable code change.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "design is frozen before implementation", Phase: executive.AcceptanceDesign},
				{Text: "implementation gates and independent review are verified", Phase: executive.AcceptanceImplementation},
			},
			Requirements: []executive.RequirementProposal{
				{Key: "design-freeze", Type: "result", Description: "design frozen by adjudication", Required: true},
				{Key: executive.MissionRequirementKey, Type: "result", Description: "engineering mission provisioned", Required: true},
				// Presence selects the internal-code mission scope; it is not a
				// separate pending approval requirement for this fixture.
				{Key: executive.InternalCodeScopeRequirementKey, Type: "condition", Description: "owner permits bounded internal code scope", Required: false},
				{Key: executive.CodeRunnerExecutionEvidenceRequirementKey, Type: "result", Description: "real CodeRunner evidence with all host gates", Required: true},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}

	root, err := h.tasks.GetTask(h.ctx, run.RootTaskID)
	if err != nil || root.Task.CorrelationID == nil {
		t.Fatalf("root lookup: task=%+v err=%v", root.Task, err)
	}
	correlationID := *root.Task.CorrelationID
	var missionTask tasks.Task
	var missionFound bool
	for pass := 0; pass < 24; pass++ {
		live, resumeErr := orchestrator.Resume(h.ctx, run.RootTaskID)
		if resumeErr != nil {
			t.Fatalf("resume before CodeRunner pass %d: run=%+v err=%v", pass, live, resumeErr)
		}
		candidates, listErr := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: "explorarte", CorrelationID: correlationID, AssignedRoleID: coderunner.RoleID, Limit: 100})
		if listErr != nil {
			t.Fatalf("list mission tasks: %v", listErr)
		}
		if len(candidates) > 0 {
			missionTask, missionFound = candidates[0], true
			break
		}
		if live.State.Terminal() || live.State == executive.StateBlocked {
			t.Fatalf("run stopped before CodeRunner mission: %+v", live)
		}
	}
	if !missionFound {
		t.Fatal("Executive did not provision a CodeRunner mission")
	}
	if missionTask.AssignedRoleID != coderunner.RoleID {
		t.Fatalf("mission assigned to %q, want %q", missionTask.AssignedRoleID, coderunner.RoleID)
	}

	worker := coderunner.Worker{
		Queue:    h.tasks,
		Executor: &coderunner.Executor{MaxOutput: 1 << 20, OperationTimeout: 30 * time.Second},
		Workspace: coderunner.StagingAdapter{
			Service:       stagingRuntime.Service,
			Tasks:         h.tasks,
			WorkspaceRoot: workspaceRoot,
			IntentResolver: engineeringmission.WorkspaceResolver{
				Tasks:        h.tasks,
				Mission:      mission,
				RepositoryID: autonomyRepositoryID,
				TargetRef:    autonomyTargetRef,
			},
		},
		PlanGuardResolver: engineeringmission.GuardResolver{Tasks: h.tasks, Mission: mission},
		WorkerID:          "executive-autonomy-smoke-code-runner",
		HolderPrincipalID: coderunner.RoleID,
		LeaseDuration:     time.Minute,
		RuntimeVersion:    "integration-autonomy-smoke",
	}
	n, err := worker.RunOnce(h.ctx)
	if err != nil || n != 1 {
		t.Fatalf("CodeRunner RunOnce: n=%d err=%v", n, err)
	}

	detail, err := h.tasks.GetTask(h.ctx, missionTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Status != tasks.StatusAwaitingVerification || len(detail.Attempts) != 1 || detail.Attempts[0].FailureCode != nil {
		t.Fatalf("CodeRunner mission did not finish cleanly: status=%s attempts=%+v", detail.Task.Status, detail.Attempts)
	}

	var workspaceID int64
	for _, evidence := range detail.Evidence {
		if !strings.HasPrefix(evidence.Reference, "code-runner-attempt-evidence://") {
			continue
		}
		candidate, ok := evidence.Metadata["candidate_revision"].(map[string]any)
		if !ok {
			continue
		}
		if id, ok := candidate["workspace_id"].(float64); ok {
			workspaceID = int64(id)
		}
	}
	if workspaceID == 0 {
		t.Fatalf("CodeRunner evidence has no workspace id: %+v", detail.Evidence)
	}
	workspace, err := stagingRuntime.Service.GetWorkspace(h.ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Status != staging.WorkspaceSealed || workspace.CandidateCommit == nil {
		t.Fatalf("workspace is not sealed with candidate commit: %+v", workspace)
	}
	candidateSHA := *workspace.CandidateCommit
	if candidateSHA == baseSHA {
		t.Fatalf("candidate commit did not differ from base %s", baseSHA)
	}
	if got := gitOutputAutonomy(t, repositoryRoot, "cat-file", "-t", candidateSHA); got != "commit" {
		t.Fatalf("candidate %s is not a commit: %q", candidateSHA, got)
	}

	promotion, err := mission.RequestPromotion(h.ctx, missionTask.ID, workspaceID, coderunner.RoleID)
	if err != nil {
		t.Fatalf("RequestPromotion: %v", err)
	}
	if promotion.Status != staging.PromotionAwaitingGates {
		t.Fatalf("promotion status=%q, want %q", promotion.Status, staging.PromotionAwaitingGates)
	}
	var approvalRequirementID int64
	for _, requirement := range detail.Requirements {
		if requirement.Key == "review" {
			approvalRequirementID = requirement.ID
		}
	}
	if approvalRequirementID == 0 {
		t.Fatal("mission review requirement is missing")
	}
	approved, err := mission.ReviewMission(h.ctx, promotion.ID, approvalRequirementID, autonomyReviewerRole, engineeringmission.Approve, "autonomous_smoke_review", "bounded candidate passed independent engineering review")
	if err != nil {
		t.Fatalf("ReviewMission: %v", err)
	}
	if approved.Status != staging.PromotionApproved {
		t.Fatalf("promotion after independent review=%q, want %q", approved.Status, staging.PromotionApproved)
	}
	if got := gitOutputAutonomy(t, repositoryRoot, "rev-parse", autonomyTargetRef); got != baseSHA {
		t.Fatalf("authoritative target moved during smoke: got=%s want=%s", got, baseSHA)
	}

	finalRun, err := runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 24)
	if err != nil || finalRun.State != executive.StateCompleted {
		t.Fatalf("final Executive run=%+v err=%v", finalRun, err)
	}
	finalRoot, err := h.tasks.GetTask(h.ctx, run.RootTaskID)
	if err != nil || finalRoot.Task.Status != tasks.StatusCompleted {
		t.Fatalf("root status=%s err=%v", finalRoot.Task.Status, err)
	}
	var linkedCodeRunnerEvidence bool
	for _, evidence := range finalRoot.Evidence {
		if strings.HasPrefix(evidence.Reference, "code-runner-attempt-evidence://") {
			linkedCodeRunnerEvidence = true
			break
		}
	}
	if !linkedCodeRunnerEvidence {
		t.Fatalf("root closed without linked CodeRunner evidence: %+v", finalRoot.Evidence)
	}
}

func initializeAutonomyRepository(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "internal", "autonomy_smoke"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunAutonomy(t, root, "init", "-b", "main")
	gitRunAutonomy(t, root, "config", "user.name", "Integration")
	gitRunAutonomy(t, root, "config", "user.email", "integration@example.invalid")
	files := map[string]string{
		"go.mod":                           "module executiveautonomyfixture\n\ngo 1.25\n",
		"Makefile":                         ".PHONY: test-kernel-governance-fitness test-executive-fitness\n\ntest-kernel-governance-fitness:\n\t@true\n\ntest-executive-fitness:\n\t@true\n",
		"internal/autonomy_smoke/value.go": "package autonomy_smoke\n\nfunc Value() string { return \"before\" }\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	gitRunAutonomy(t, root, "add", "--", "go.mod", "Makefile", autonomyChangePath)
	gitRunAutonomy(t, root, "commit", "-m", "base")
	base := gitOutputAutonomy(t, root, "rev-parse", "HEAD")
	gitRunAutonomy(t, root, "checkout", "--detach", base)
	return base
}

func openAutonomyStagingRuntime(t *testing.T, store *platformpostgres.Store, repositoryRoot string) (*stagingbootstrap.Runtime, string, staging.RepositoryConfig) {
	t.Helper()
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	quarantineRoot := filepath.Join(t.TempDir(), "quarantine")
	catalogFile := filepath.Join(t.TempDir(), "repositories.yaml")
	catalogBody := "schema_version: 1\nrepositories:\n  - id: " + autonomyRepositoryID + "\n    path: " + repositoryRoot + "\n    enabled: true\n    allowed_target_refs:\n      - " + autonomyTargetRef + "\n"
	if err := os.WriteFile(catalogFile, []byte(catalogBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Registry: config.RegistryConfig{CanonicalDir: filepath.Join("..", "..", "docs", "canonical"), SyncTimeout: 30 * time.Second},
		Tasks:    config.TaskConfig{OrganizationID: "explorarte", OutboxMaxAttempts: 3},
		Staging: config.StagingConfig{
			Enabled: true, RepositoriesFile: catalogFile, WorkspaceRoot: workspaceRoot,
			ArtifactRoot: artifactRoot, QuarantineRoot: quarantineRoot, CommandTimeout: time.Minute,
			MaxArtifactBytes: 16 << 20, MaxChangedFiles: 100, StaleAfter: 2 * time.Minute,
			ReconcileInterval: time.Minute, ReconcileBatchSize: 100, GitBinary: "git",
		},
	}
	runtime, err := stagingbootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open staging runtime: %v", err)
	}
	repository, _, err := runtime.Catalog.Get(context.Background(), autonomyRepositoryID)
	if err != nil {
		t.Fatalf("get fixture repository: %v", err)
	}
	return runtime, workspaceRoot, repository
}

func gitRunAutonomy(t *testing.T, directory string, args ...string) {
	t.Helper()
	if output, err := testsupport.RunGit(directory, args...); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutputAutonomy(t *testing.T, directory string, args ...string) string {
	t.Helper()
	output, err := testsupport.RunGit(directory, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
