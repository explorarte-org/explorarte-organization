//go:build integration

package engineeringmission_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	modelruntimepostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// admitEverything stands in for the program budget so this test can isolate the
// seam it exists to cross. Budget admission has its own unit coverage
// (TestRecoveryEligibility) and its SQL is executed for real at the end of this
// test, against the real schema.
type admitEverything struct{}

func (admitEverything) Admit(context.Context, int64) (engineeringmission.BudgetVerdict, error) {
	return engineeringmission.BudgetVerdict{Admitted: true}, nil
}

// A recovery successor has to be a REAL engineering mission, not a task that
// merely looks like one.
//
// This is the seam adversarial review found unguarded, and it was unguarded
// because the two suites either side of it each tested their own half: the
// task-engine tests drove plain tasks through redrive, and the policy tests
// used a fake that never persisted anything. Between them, "the successor is a
// mission Mission.Resolve can read back, pinned to the new head, claimable
// only once that is durable" was asserted by nobody.
func TestRecoverySuccessorIsAResolvableMissionPostgreSQL(t *testing.T) {
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
	stagingRuntime, taskService, _ := openMissionRuntime(t, ctx, store, repositoryRoot)
	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}

	policy := engineeringmission.MissionPolicy{
		BaseSHA:            baseSHA,
		Objective:          "bump the fixture value inside the allowed boundary",
		AllowedPaths:       []string{"fixture"},
		AcceptanceCriteria: []string{"fixture tests pass"},
		RequiredGates: []engineeringmission.RequiredGate{
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
`, "")
	failed, err := mission.Create(ctx, policy, plan, "explorarte", reviewerRole, "human", "eduardo")
	if err != nil {
		t.Fatalf("create mission: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE tasks SET max_attempts=1 WHERE id=$1`, failed.ID); err != nil {
		t.Fatal(err)
	}

	// Run the mission far enough to record the world it inhabited -- the
	// workspace is what recovery reads to learn this mission's repository,
	// target ref and base commit -- then exhaust it with a transient
	// failure the runtime classifies as retryable.
	claimed, err := taskService.ClaimTaskByID(ctx, failed.ID, tasks.ClaimRequest{
		WorkerID:       "recovery-runner",
		AssignedRoleID: coderunner.RoleID, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("claim mission: %v", err)
	}
	lease := tasks.LeaseCommand{TaskID: failed.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "recovery-runner"}
	if _, err := taskService.StartAttempt(ctx, lease); err != nil {
		t.Fatal(err)
	}
	detail, err := taskService.GetTask(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	var artifactRequirement int64
	for _, requirement := range detail.Requirements {
		if requirement.Key == "candidate-artifact" {
			artifactRequirement = requirement.ID
		}
	}
	if _, err := stagingRuntime.Service.CreateWorkspace(ctx, staging.CreateWorkspaceCommand{
		TaskID: failed.ID, AttemptID: claimed.Attempt.ID, RepositoryID: repositoryID,
		BaseCommit: baseSHA, TargetRef: targetRef, HolderID: "recovery-runner",
		ActorRoleID: coderunner.RoleID, ArtifactRequirementID: artifactRequirement,
		LeaseToken: claimed.LeaseToken,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	dead, err := taskService.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: lease,
		Result:       tasks.AttemptResult{Outcome: tasks.OutcomeRetryableFailure, FailureCode: "execution_failed", Summary: "gate failed"},
	})
	if err != nil || dead.Status != tasks.StatusDeadLetter {
		t.Fatalf("drive to dead letter: status=%q err=%v", dead.Status, err)
	}
	letters, err := taskService.ListDeadLetters(ctx, 10)
	if err != nil || len(letters) != 1 {
		t.Fatalf("letters=%+v err=%v", letters, err)
	}

	invocations, err := modelruntimepostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	recovery := engineeringmission.Recovery{
		Tasks:      taskService,
		Mission:    mission,
		Head:       engineeringmission.StagingHead{Catalog: stagingRuntime.Catalog, Backend: stagingRuntime.Git},
		Workspaces: stagingRuntime.Service,
		Ambiguity:  invocations,
		Budget:     admitEverything{},

		MaxRecoveryEpisodes: 1,
		ActorType:           "system",
		ActorID:             "recovery-runner",
	}

	// Before the world moves, the answer must be no.
	decision, _, err := recovery.Recover(ctx, letters[0])
	if err != nil {
		t.Fatalf("evaluate before the head moved: %v", err)
	}
	if decision.Reason != engineeringmission.RecoveryUnchangedWorld {
		t.Fatalf("reason=%q, want unchanged_world while the target still points at the failed base", decision.Reason)
	}

	movedHead := advanceMissionRepository(t, repositoryRoot)
	if movedHead == baseSHA {
		t.Fatal("the fixture commit did not move the target")
	}

	decision, result, err := recovery.Recover(ctx, letters[0])
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if decision.Reason != engineeringmission.RecoveryEligible || !result.Created {
		t.Fatalf("decision=%+v result=%+v", decision, result)
	}
	if decision.TargetRef != targetRef || decision.RepositoryID != repositoryID {
		t.Fatalf("recovery consulted %s@%s, want the mission's own world", decision.RepositoryID, decision.TargetRef)
	}

	// THE SEAM: the successor must read back as a mission, pinned to the
	// head that justified it.
	recovered, err := mission.Resolve(ctx, result.Successor.ID)
	if err != nil {
		t.Fatalf("the successor must be a resolvable engineering mission: %v", err)
	}
	if recovered.BaseSHA != movedHead {
		t.Fatalf("successor base_sha=%q, want the moved head %q", recovered.BaseSHA, movedHead)
	}
	if recovered.TaskID != result.Successor.ID {
		t.Fatalf("successor policy task_id=%d, want %d", recovered.TaskID, result.Successor.ID)
	}
	if len(recovered.AllowedPaths) != 1 || recovered.AllowedPaths[0] != "fixture" {
		t.Fatalf("the successor must inherit the mission boundary, got %v", recovered.AllowedPaths)
	}
	if len(recovered.RequiredGates) != 1 {
		t.Fatalf("the successor must inherit the required gates, got %v", recovered.RequiredGates)
	}

	// And it must be claimable only now -- published, not born runnable.
	successor, err := taskService.GetTask(ctx, result.Successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if successor.Task.Status != tasks.StatusReady {
		t.Fatalf("successor status=%q, want ready once its policy is durable", successor.Task.Status)
	}

	// The guard that would have caught the original defect: the guard
	// resolver is what a real worker consults before executing, and it goes
	// through Mission.Resolve. A successor carrying no policy fails here.
	if _, err := (engineeringmission.GuardResolver{Tasks: taskService, Mission: mission}).ResolveGuard(ctx, tasks.ClaimedTask{
		Task: successor.Task,
	}); err != nil {
		t.Fatalf("a worker must be able to resolve the successor's guard: %v", err)
	}

	// The failed mission stays terminal and is now linked to its successor.
	original, err := taskService.GetTask(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if original.Task.Status != tasks.StatusDeadLetter {
		t.Fatalf("the recovered mission must stay dead_letter, got %q", original.Task.Status)
	}
	stamped, err := taskService.GetDeadLetter(ctx, letters[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stamped.RedriveTaskID == nil || *stamped.RedriveTaskID != result.Successor.ID {
		t.Fatalf("dead letter must point at the successor, got %v", stamped.RedriveTaskID)
	}

	// Execute the admission SQL against the real schema. The verdict is not
	// the point -- that a hand-written query with a shared fragment still
	// binds and runs is.
	ledger, err := costledgerpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.TaskFamilySpend(ctx, failed.ID, "deepseek", []string{"deepseek-chat"}); err != nil {
		t.Fatalf("task family spend query: %v", err)
	}
	if _, err := ledger.ProgramFamilySpend(ctx, "any-correlation", "deepseek", []string{"deepseek-chat"}); err != nil {
		t.Fatalf("program family spend query: %v", err)
	}
	if _, err := invocations.UnreconciledAmbiguousInvocations(ctx, failed.ID); err != nil {
		t.Fatalf("ambiguity query: %v", err)
	}
}

// advanceMissionRepository lands a commit on the mission's target ref, which
// is the only thing that can make a repeat of the failed work plausible.
//
// The repository is left detached exactly as initializeMissionRepository left
// it, so staging's refusal to operate on a checked-out target keeps holding.
func advanceMissionRepository(t *testing.T, root string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "fixture", "value.go"),
		[]byte("package fixture\n\nfunc Value() int { return 41 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunEM(t, root, "add", "--", "fixture/value.go")
	gitRunEM(t, root, "commit", "-m", "advance the target past the failed base")
	head := gitOutputEM(t, root, "rev-parse", "HEAD")
	gitRunEM(t, root, "branch", "-f", "main", head)
	gitRunEM(t, root, "checkout", "--detach", head)
	return head
}
