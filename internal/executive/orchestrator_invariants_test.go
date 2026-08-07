package executive

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func testOrchestratorForPorts(t *testing.T, tasksPort *memoryTasks, models *fakeModels, completion *fakeCompletion) *Orchestrator {
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
	value, err := NewOrchestrator(
		"explorarte", registry, tasksPort, &fakeContexts{}, fakeAssignments{}, models, completion,
		allowAuthz{}, DefaultLimits(), ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	)
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

func TestOneInvocationPerTaskAttempt(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	models.ensure = InvocationRecord{ID: 500, Status: "requested"}
	orchestrator := testOrchestratorForPorts(t, tasksPort, models, &fakeCompletion{verdict: CompletionPass})
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
	if _, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, "department_plan", func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	if _, err := orchestrator.driveTypedTask(context.Background(), root, current, departmentPlanOutputSchema, "department_plan", func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if models.ensureCalls != 1 {
		t.Fatalf("EnsureInvocation calls=%d", models.ensureCalls)
	}
	current, _ = tasksPort.GetTask(context.Background(), task.ID)
	attemptID := current.Attempts[0].ID
	if got := len(models.invocations[invocationKey(task.ID, attemptID)]); got != 1 {
		t.Fatalf("invocations=%d", got)
	}
}

func TestAmbiguousBlocksWithoutRetry(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	models.ensure = InvocationRecord{ID: 501, Status: "ambiguous"}
	orchestrator := testOrchestratorForPorts(t, tasksPort, models, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-a",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:a",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-a",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, "department_plan", func(InvocationResult) error { return nil })
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("err=%v", err)
	}
	if models.ensureCalls != 1 {
		t.Fatalf("EnsureInvocation calls=%d", models.ensureCalls)
	}
	blocked := tasksPort.tasks[root.ID]
	if blocked.Status != "blocked" || blocked.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("root=%+v", blocked)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	attemptID := current.Attempts[0].ID
	if got := len(models.invocations[invocationKey(task.ID, attemptID)]); got != 1 {
		t.Fatalf("invocations=%d", got)
	}
}

func TestToolIntentsRejectedForPlanning(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	models.ensure = InvocationRecord{ID: 502, Status: "succeeded"}
	models.results[502] = InvocationResult{
		InvocationID: 502,
		JSONOutput: json.RawMessage(`{"schema_version":"department-plan/v1","department_id":"ingenieria_ia","tasks":[],"review_criteria":[],"unresolved":[]}`),
		ToolIntents: 1, ResponseHash: actionDigest("result"), ResponseBytes: 128,
	}
	completion := &fakeCompletion{verdict: CompletionPass}
	orchestrator := testOrchestratorForPorts(t, tasksPort, models, completion)
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-tool",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:tool",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-tool",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	_, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, "department_plan", func(InvocationResult) error { return nil })
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
		SuccessCriteria: []string{"x"},
	}
	if err := orchestrator.validateRunCompletionEvidence(context.Background(), root, plan); err == nil {
		t.Fatal("expected evidence conflict")
	}
}

func TestInvocationCorrelationAndCausationPropagate(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	models.ensure = InvocationRecord{ID: 700, Status: "requested"}
	orchestrator := testOrchestratorForPorts(t, tasksPort, models, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-cause",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:cause",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-cause",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		CausationID: taskCausation(root.ID), Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	if _, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, "department_plan", func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	attemptID := current.Attempts[0].ID
	values := models.invocations[invocationKey(task.ID, attemptID)]
	if len(values) != 1 {
		t.Fatalf("invocations=%d", len(values))
	}
	if values[0].CorrelationID != root.CorrelationID || values[0].CausationID != attemptCausation(task.ID, attemptID) {
		t.Fatalf("invocation=%+v", values[0])
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
