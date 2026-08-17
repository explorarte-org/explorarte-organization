package executive

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func testOrchestratorForPorts(t *testing.T, tasksPort *memoryTasks, models *fakeModels, completion *fakeCompletion, opts ...OrchestratorOption) *Orchestrator {
	t.Helper()
	return testOrchestratorWithHarness(t, tasksPort, models, newFakeHarness(models), &countingBudget{}, completion, opts...)
}

func testOrchestratorWithHarness(t *testing.T, tasksPort *memoryTasks, models *fakeModels, harness HarnessExecutor, budget ModelBudgetGate, completion *fakeCompletion, opts ...OrchestratorOption) *Orchestrator {
	t.Helper()
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	registry := fakeRegistry{
		rev: RevisionRef{ID: 7},
		units: map[string]UnitRef{
			"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID},
		},
		roles:   map[string]RoleRef{leader.ID: leader},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	value, err := NewOrchestrator(Dependencies{
		OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort, Contexts: &fakeContexts{},
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: budget, Completion: completion,
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSubmitIdempotencyAndCorrelation(t *testing.T) {
	tasksPort := newMemoryTasks()
	orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), &fakeCompletion{verdict: CompletionPass})
	request := SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "owner-goal-001",
		Goal: OwnerGoal{Goal: "Analyze one area", AcceptanceCriteria: []string{"produce a plan"}},
	}
	first, reused, err := orchestrator.Submit(context.Background(), request)
	if err != nil || reused {
		t.Fatalf("first submit reused=%v err=%v", reused, err)
	}
	second, reused, err := orchestrator.Submit(context.Background(), request)
	if err != nil || !reused {
		t.Fatalf("second submit reused=%v err=%v", reused, err)
	}
	if first.RootTaskID != second.RootTaskID || first.CorrelationID == "" || first.CorrelationID != second.CorrelationID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	root := tasksPort.tasks[first.RootTaskID]
	if root.RequestedByRoleID != OwnerRoleID || root.AssignedRoleID != CEORoleID {
		t.Fatalf("root requester/assignee=%s/%s", root.RequestedByRoleID, root.AssignedRoleID)
	}
	if root.CausationID != "owner:"+request.IdempotencyKey {
		t.Fatalf("causation=%q", root.CausationID)
	}
}

// TestOneHarnessRunPerTaskAttempt: one attempt produces one Harness run and
// one durable invocation, and re-driving the same task does not produce a
// second of either.
func TestOneHarnessRunPerTaskAttempt(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:x",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"},
		CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	if _, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	if _, err := orchestrator.driveTypedTask(context.Background(), root, current, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if harness.callCount() != 1 {
		t.Fatalf("harness runs=%d want 1", harness.callCount())
	}
	current, _ = tasksPort.GetTask(context.Background(), task.ID)
	attemptID := current.Attempts[0].ID
	if got := models.invocationCount(task.ID, attemptID); got != 1 {
		t.Fatalf("invocations=%d", got)
	}
}

// TestAmbiguousBlocksWithoutRetry: the Harness reports a model failure, but
// the durable Model Runtime invocation says the provider outcome is ambiguous.
// Model Runtime is the owner of that fact, so the run blocks for explicit
// reconciliation and no second provider call is made.
func TestAmbiguousBlocksWithoutRetry(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.invocationStatus = "ambiguous"
	harness.failure = HarnessFailureModelError
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-a",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:a",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-a",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil })
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("err=%v", err)
	}
	if harness.callCount() != 1 {
		t.Fatalf("harness runs=%d want 1", harness.callCount())
	}
	blocked, _ := tasksPort.GetTask(context.Background(), root.ID)
	if blocked.Status != "blocked" || blocked.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("root=%+v", blocked)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	attemptID := current.Attempts[0].ID
	if got := models.invocationCount(task.ID, attemptID); got != 1 {
		t.Fatalf("invocations=%d", got)
	}
	// Driving again must not reach the provider a second time.
	if _, secondErr := orchestrator.driveTypedTask(context.Background(), root, current, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil }); !errors.Is(secondErr, ErrModelOutcomeAmbiguous) {
		t.Fatalf("second drive err=%v", secondErr)
	}
	if harness.callCount() != 1 {
		t.Fatalf("harness runs after re-drive=%d want 1", harness.callCount())
	}
}

func TestToolIntentsRejectedForPlanning(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.toolIntents = 1
	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, completion)
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-tool",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:tool",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-tool",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil })
	if !errors.Is(err, ErrToolIntentRejected) {
		t.Fatalf("err=%v", err)
	}
	if completion.calls != 0 || len(tasksPort.finalized) != 0 {
		t.Fatalf("completion=%d finalized=%v", completion.calls, tasksPort.finalized)
	}
}

func TestCompletionGateMappings(t *testing.T) {
	cases := []struct {
		name    string
		verdict CompletionVerdict
		status  string
		err     error
	}{
		{name: "pass", verdict: CompletionPass, status: "completed"},
		{name: "fail", verdict: CompletionFail, status: "failed", err: ErrCompletionFailed},
		{name: "inconclusive", verdict: CompletionInconclusive, status: "blocked", err: ErrCompletionInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tasksPort := newMemoryTasks()
			completion := &fakeCompletion{verdict: tc.verdict}
			orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), completion)
			task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
				RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "gate-" + tc.name,
				Title: "gate", Instructions: "gate", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:gate",
			})
			task.Status = "awaiting_verification"
			task.Attempts = []AttemptRecord{{ID: 42, Ordinal: 1, State: "finished"}}
			tasksPort.tasks[task.ID] = task
			got, err := orchestrator.gatedComplete(context.Background(), task)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want=%v", err, tc.err)
			}
			if got.Status != tc.status {
				t.Fatalf("status=%s want=%s", got.Status, tc.status)
			}
			if completion.calls != 1 {
				t.Fatalf("verify calls=%d", completion.calls)
			}
			if tc.verdict != CompletionPass && len(tasksPort.finalized) != 0 {
				t.Fatalf("unexpected FinalCompleted: %v", tasksPort.finalized)
			}
		})
	}
}

func TestCEOCompletedClaimCannotOverrideReviewEvidence(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	orchestrator := testOrchestratorForPorts(t, tasksPort, models, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-lie",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:lie",
	})
	review, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: childKey(root.ID, "leader-review:ingenieria_ia"), Title: "review", Instructions: "review",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
	})
	review.Status = "completed"
	review.Attempts = []AttemptRecord{{ID: 88, Ordinal: 1, State: "finished"}}
	tasksPort.tasks[review.ID] = review
	models.invocations[invocationKey(review.ID, 88)] = []InvocationRecord{{ID: 888, TaskID: review.ID, AttemptID: 88, Status: "succeeded"}}
	models.results[888] = InvocationResult{InvocationID: 888, JSONOutput: json.RawMessage(`{"schema_version":"department-review/v1","verdict":"blocked","findings":["blocked"],"unsatisfied_criteria":["x"],"evidence_refs":[],"proposed_followup_tasks":[]}`)}
	plan := ExecutivePlan{
		SchemaVersion: ExecutivePlanSchemaVersion, Objective: "x",
		DepartmentRequests: []DepartmentRequest{{UnitID: "ingenieria_ia", Objective: "x", Deliverable: "x"}},
		SuccessCriteria:    []string{"x"},
	}
	if err := orchestrator.validateRunCompletionEvidence(context.Background(), root, plan); err == nil {
		t.Fatal("expected evidence conflict")
	}
}

func TestInvocationCorrelationAndCausationPropagate(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-cause",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:cause",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-cause",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		CausationID: taskCausation(root.ID), Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	if _, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	attemptID := current.Attempts[0].ID
	values, _ := models.FindTaskAttemptInvocations(context.Background(), task.ID, attemptID)
	if len(values) != 1 {
		t.Fatalf("invocations=%d", len(values))
	}
	if values[0].CorrelationID != root.CorrelationID || values[0].CausationID != attemptCausation(task.ID, attemptID) {
		t.Fatalf("invocation=%+v", values[0])
	}
	command := harness.lastCommand()
	if command.CorrelationID != root.CorrelationID || command.CausationID != attemptCausation(task.ID, attemptID) {
		t.Fatalf("harness command=%+v", command)
	}
}

func TestObserverAndDailyCycleHaveNoExecutionPath(t *testing.T) {
	if ObserverRoleID == CEORoleID {
		t.Fatal("observer must be distinct")
	}
	orchestratorType := reflect.TypeOf(Orchestrator{})
	for i := 0; i < orchestratorType.NumField(); i++ {
		name := orchestratorType.Field(i).Name
		if name == "scheduler" || name == "observer" || name == "dailyCycle" {
			t.Fatalf("unexpected productive field %s", name)
		}
	}
}
