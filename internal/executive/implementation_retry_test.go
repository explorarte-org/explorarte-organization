package executive

import (
	"context"
	"strings"
	"testing"
)

// Local smoke #40 (root 1990): the mission failed `go vet` (check_failed) on a compile error in the
// planner's own test, and the root blocked although the frozen design was sound. Deterministic
// mission failures return to the implementation plan, bounded, with their evidence.

const failedPlanJSON = `{"schema_version":"code-runner-execution/v1","operations":[{"type":"APPLY_PATCH","patch":"--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"}]}`

func retryFixture(reason string, extraMissions ...EvidenceRecord) (*Orchestrator, TaskRecord) {
	tasks := newMemoryTasks()
	root := codeRunnerRootForTest()
	root.Evidence = append([]EvidenceRecord{{Reference: "engineering-mission://91", Type: "result"}}, extraMissions...)
	tasks.tasks[root.ID] = root
	mission := codeRunnerTaskForTest()
	mission.Status, mission.ReasonCode = "failed", reason
	mission.Reason = "operation GO_VET failed with exit code 1: blockedClaim does not implement RootClaim (missing method SessionPID)"
	mission.Instructions = failedPlanJSON
	tasks.tasks[mission.ID] = mission
	return &Orchestrator{tasks: tasks}, root
}

func TestADeterministicMissionFailureReturnsToThePlanWithItsEvidence(t *testing.T) {
	o, root := retryFixture("check_failed")
	attempt, due, err := o.implementationRetryDue(context.Background(), root)
	if err != nil || !due {
		t.Fatalf("due=%v err=%v, want a retry after check_failed", due, err)
	}
	if attempt.Ordinal != 1 || attempt.SupersedesMission != 91 || attempt.planKey() != "implementation-plan:retry:1" {
		t.Fatalf("attempt %+v", attempt)
	}
	for _, want := range []string{"PREVIOUS IMPLEMENTATION FAILED", "missing method SessionPID", "PATCH THAT FAILED", "+b", "Keep the frozen design"} {
		if !strings.Contains(attempt.RetryContext, want) {
			t.Errorf("retry context lacks %q:\n%s", want, attempt.RetryContext)
		}
	}
}

func TestOnlyDeterministicFailuresAndOnlyBoundedRetries(t *testing.T) {
	if o, root := retryFixture("execution_failed"); true {
		if _, due, _ := o.implementationRetryDue(context.Background(), root); due {
			t.Error("a transient failure was sent back to the planner")
		}
	}
	// Two retries already made: 91 superseded by 92, 92 by 93; 93 is the latest and failed.
	o, root := retryFixture("check_failed",
		EvidenceRecord{Reference: "engineering-mission://92", Metadata: map[string]any{SupersedesMissionKey: float64(91)}},
		EvidenceRecord{Reference: "engineering-mission://93", Metadata: map[string]any{SupersedesMissionKey: float64(92)}})
	memory := o.tasks.(*memoryTasks)
	failed := memory.tasks[91]
	failed.ID = 93
	memory.tasks[93] = failed
	if _, due, _ := o.implementationRetryDue(context.Background(), root); due {
		t.Fatalf("a %d-th retry was allowed, at most %d", 3, MaxImplementationRetries)
	}
}

func TestTheLatestMissionOfARetryChainIsTheRootsMission(t *testing.T) {
	chain := TaskRecord{Evidence: []EvidenceRecord{
		{Reference: "engineering-mission://91"},
		{Reference: "engineering-mission://95", Metadata: map[string]any{SupersedesMissionKey: float64(91)}},
	}}
	if id, err := missionTaskID(chain); err != nil || id != 95 {
		t.Fatalf("chain: mission %d err %v, want 95", id, err)
	}
	unrelated := TaskRecord{Evidence: []EvidenceRecord{{Reference: "engineering-mission://91"}, {Reference: "engineering-mission://95"}}}
	if _, err := missionTaskID(unrelated); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("two unrelated missions were accepted: %v", err)
	}
}

// Through the mission phase: the retry creates a new implementation plan task carrying the frozen
// design and the failure, and asks nothing of the design loop.
func TestARetryPlansAgainFromTheFrozenDesign(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	root := fixture.rootRecord(t)
	requirement, _ := findRequirementByKey(root.Requirements, MissionRequirementKey)
	designTasksBefore := countDesignTasks(all)
	attempt := implementationAttempt{Ordinal: 1, SupersedesMission: 900, RetryContext: "\n\nPREVIOUS IMPLEMENTATION FAILED (mission task 900, check_failed). FAILURE:\nmissing method SessionPID"}
	if _, _, err := fixture.orchestrator.driveMissionFromPlan(context.Background(), root, all, requirement, attempt); err != nil {
		t.Fatalf("retry: %v", err)
	}
	all, _ = fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	retry, found := findTaskByKey(all, childKey(root.ID, "implementation-plan:retry:1"))
	if !found {
		t.Fatal("no retry implementation plan was created")
	}
	for _, want := range []string{"FROZEN DESIGN", "missing method SessionPID", DesignFileManifestKey} {
		if !strings.Contains(retry.Instructions, want) {
			t.Errorf("retry plan instructions lack %q", want)
		}
	}
	if got := countDesignTasks(all); got != designTasksBefore {
		t.Fatalf("the retry touched the design loop: %d design tasks, was %d", got, designTasksBefore)
	}

	// Driven to provisioning, the retry makes a NEW mission (root 1990: missions are created
	// idempotently by policy digest, and an identical policy resolved to the failed mission), and
	// its evidence names the mission it supersedes.
	before := fixture.provisioner.count()
	for i := 0; i < 6 && fixture.provisioner.count() == before; i++ {
		all, _ = fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
		root = fixture.rootRecord(t)
		if _, _, err := fixture.orchestrator.driveMissionFromPlan(context.Background(), root, all, requirement, attempt); err != nil {
			t.Fatalf("retry pass %d: %v", i, err)
		}
	}
	if fixture.provisioner.count() != before+1 {
		t.Fatalf("missions %d, want a new one beside the first %d", fixture.provisioner.count(), before)
	}
	superseding := false
	for _, evidence := range fixture.rootRecord(t).Evidence {
		if strings.HasPrefix(evidence.Reference, "engineering-mission://") && evidence.Metadata[SupersedesMissionKey] != nil {
			superseding = true
		}
	}
	if !superseding {
		t.Fatal("the retry's mission evidence does not name the mission it supersedes")
	}
}

func countDesignTasks(all []TaskRecord) int {
	n := 0
	for _, task := range all {
		switch task.TaskClass {
		case TaskClassCoordinationDeptReview, TaskClassCoordinationDeptPlan:
			n++
		}
		if strings.Contains(task.IdempotencyKey, ":worker:") || strings.Contains(task.IdempotencyKey, "design-review") || strings.Contains(task.IdempotencyKey, "design-adjudication") {
			n++
		}
	}
	return n
}
