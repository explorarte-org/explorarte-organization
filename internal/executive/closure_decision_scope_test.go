package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// CEO_CLOSURE_DECISION_CONTEXT_FIX_V1.
//
// Two independent defects, both traced by
// CEO_CLOSURE_DECISION_CONTEXT_CONTAMINATION_FORENSICS_V1 against real root
// 629 evidence:
//
//   - FIX A: createClosureTask carried no equivalent of CEO-plan's
//     OWNER_DECISION_POLICY, so nothing told the closure model that
//     blocked_items/unresolved_decisions must be grounded in THIS root's own
//     goal rather than in canonical/organization-global/historical material.
//   - FIX B: a closure claiming status="completed" while ALSO reporting
//     non-empty blocked_items or unresolved_decisions is a self-contradictory
//     model result, but it was accepted as a valid, successful attempt --
//     the contradiction was caught only later, when Resume re-reads the
//     already-completed closure task and blocks the root
//     (executive_closure_not_complete, orchestrator.go, UNCHANGED by this
//     fix). This meant the model never got a retry chance to correct itself.
//
// closureFixture below drives the REAL Orchestrator (in-memory ports, no
// network) through CEO-plan -> department-plan -> department-worker ->
// department-review -> CEO-closure, exactly as production does, with a
// scriptable, mutable closure body so a single fixture can exercise every
// shape.

type closureFixture struct {
	orchestrator *Orchestrator
	tasks        *memoryTasks
	harness      *scriptedHarness
	root         int64
}

const (
	closureFixtureDepartmentPlanBody = `{"schema_version":"department-plan/v1","department_id":"ingenieria_ia",` +
		`"tasks":[{"client_key":"audit-1","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.design",` +
		`"title":"Run the audit","instructions":"Do the audit.","acceptance_criteria":["done"],` +
		`"dependencies":[],"requirements":[],"priority":50}],"review_criteria":["done"],"unresolved":[]}`

	closureFixtureWorkerBody = `{"schema_version":"worker-result/v1","summary":"Audit complete.","evidence_refs":[]}`

	closureFixtureReviewBody = `{"schema_version":"department-review/v1","verdict":"accept","findings":["ok"],` +
		`"unsatisfied_criteria":[],"evidence_refs":["task:1:context"],"proposed_followup_tasks":[]}`

	ceoPlanFixtureBody = `{"schema_version":"executive-plan/v1","objective":"Audit",` +
		`"department_requests":[{"unit_id":"ingenieria_ia","objective":"audit","deliverable":"report","priority":1,"constraints":[]}],` +
		`"global_constraints":[],"success_criteria":["done"],"owner_decisions_required":[]}`

	closureCompletedClean = `{"schema_version":"executive-closure/v1","status":"completed",` +
		`"answer_to_owner":"All good.","completed_items":["done"],"blocked_items":[],"unresolved_decisions":[],` +
		`"evidence_refs":["task:1:context"]}`

	closureCompletedWithBlockedItems = `{"schema_version":"executive-closure/v1","status":"completed",` +
		`"answer_to_owner":"Claims done but blocked.","completed_items":["done"],` +
		`"blocked_items":["cannot certify deployed version from available evidence"],"unresolved_decisions":[],` +
		`"evidence_refs":["task:1:context"]}`

	closureCompletedWithUnresolvedDecisions = `{"schema_version":"executive-closure/v1","status":"completed",` +
		`"answer_to_owner":"Claims done but unresolved.","completed_items":["done"],"blocked_items":[],` +
		`"unresolved_decisions":["an unrelated historical decision surfaced from canonical context"],` +
		`"evidence_refs":["task:1:context"]}`

	closureBlockedWithBlockedItems = `{"schema_version":"executive-closure/v1","status":"blocked",` +
		`"answer_to_owner":"Genuinely blocked.","completed_items":[],` +
		`"blocked_items":["a real blocker discovered while executing this root's own work"],` +
		`"unresolved_decisions":[],"evidence_refs":["task:1:context"]}`
)

func newClosureFixture(t *testing.T) *closureFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	acceptance := newMemoryAcceptance()
	models := newFakeModels()
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: map[ExecutionPurpose]string{
		PurposeCEOPlan:          ceoPlanFixtureBody,
		PurposeDepartmentPlan:   closureFixtureDepartmentPlanBody,
		PurposeDepartmentWorker: closureFixtureWorkerBody,
		PurposeDepartmentReview: closureFixtureReviewBody,
		PurposeCEOClosure:       closureCompletedClean,
	}}

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	worker := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	registry := fakeRegistry{
		rev:     RevisionRef{ID: 7},
		units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
		roles:   map[string]RoleRef{leader.ID: leader, worker.ID: worker, ceo.ID: ceo},
		leaders: map[string]RoleRef{"ingenieria_ia": leader},
	}
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: acceptance,
		OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort, Contexts: &fakeContexts{},
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(629, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "closure-fixture-" + t.Name(),
		Goal: OwnerGoal{
			Goal: "Audit-only goal, no code changes, no git promotion.",
			AcceptanceCriteria: []AcceptanceCriterion{
				{Text: "design scoped to analysis only", Phase: AcceptanceDesign},
				{Text: "report describes operational state", Phase: AcceptanceImplementation},
			},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &closureFixture{orchestrator: orchestrator, tasks: tasksPort, harness: harness, root: run.RootTaskID}
}

// drive resumes until the run reaches a terminal/blocked state or maxPasses
// is exhausted, tolerating the ordinary set of non-fatal Resume errors.
func (f *closureFixture) drive(t *testing.T, maxPasses int) {
	t.Helper()
	for i := 0; i < maxPasses; i++ {
		run, err := f.orchestrator.Resume(context.Background(), f.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrTaskRetryScheduled) &&
			!errors.Is(err, ErrModelResultContractRejected) {
			t.Fatalf("resume %d: %v", i, err)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			return
		}
	}
}

func (f *closureFixture) rootRecord(t *testing.T) TaskRecord {
	t.Helper()
	root, err := f.tasks.GetTask(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func findClosureTask(t *testing.T, tasksPort *memoryTasks, correlation string) TaskRecord {
	t.Helper()
	values, err := tasksPort.ListByCorrelation(context.Background(), correlation)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range values {
		if task.TaskClass == TaskClassCoordinationCEOClosure {
			return task
		}
	}
	t.Fatal("no CEO closure task found")
	return TaskRecord{}
}

// TEST 1 -- CLOSURE INSTRUCTION SCOPE (general; no D-002/D-003/D-004 hardcoding).
//
// Recreates the RED test from forensics: closure instructions/acceptance
// criteria must state that canonical/global/open decisions are not
// automatically applicable, and that blocked_items/unresolved_decisions must
// be grounded in the current root's own goal. Must pass after FIX A.
func TestClosureInstructionsScopeBlockedAndUnresolvedToCurrentGoal(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	orch := testOrchestratorWithHarness(t, tasksPort, models, newFakeHarness(models), &countingBudget{}, &fakeCompletion{verdict: CompletionPass})

	root, _, err := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "closure-scope-root",
		Title: "root", Instructions: "goal", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:closure-scope",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := ExecutivePlan{Objective: "test objective", SuccessCriteria: []string{"x"}}

	closureTask, _, err := orch.createClosureTask(context.Background(), root, plan, nil)
	if err != nil {
		t.Fatal(err)
	}

	combined := strings.ToLower(closureTask.Instructions + "\n" + strings.Join(closureTask.AcceptanceCriteria, "\n"))
	for _, want := range []string{"historical", "current owner goal", "this root's own goal", "reactivate"} {
		if strings.Contains(combined, want) {
			continue
		}
	}
	// At least the two invariant anchors -- "reactivate" (the prohibition)
	// and either applicability phrase (the scope) -- must be present.
	if !strings.Contains(combined, "reactivate") {
		t.Errorf("closure instructions/acceptance_criteria must forbid reactivating historical/canonical/unrelated decisions (missing %q)\ninstructions=%q\nacceptance_criteria=%v",
			"reactivate", closureTask.Instructions, closureTask.AcceptanceCriteria)
	}
	if !strings.Contains(combined, "this root's own goal") && !strings.Contains(combined, "current owner goal") {
		t.Errorf("closure instructions/acceptance_criteria must scope blocked_items/unresolved_decisions to the current root's own goal\ninstructions=%q\nacceptance_criteria=%v",
			closureTask.Instructions, closureTask.AcceptanceCriteria)
	}
}

// TEST: CEO-plan's own policy must survive FIX A unchanged in substance
// (still reachable, still scopes owner_decisions_required the same way).
func TestCEOPlanPolicyPreservedAfterSharedExtraction(t *testing.T) {
	if !strings.Contains(ceoPlanInstructionPrefix, "OWNER_DECISION_POLICY") {
		t.Fatal("ceoPlanInstructionPrefix must still carry OWNER_DECISION_POLICY")
	}
	for _, want := range []string{"reactivate", "owner_decisions_required"} {
		if !strings.Contains(ceoPlanInstructionPrefix, want) {
			t.Fatalf("ceoPlanInstructionPrefix missing %q after extraction", want)
		}
	}
}

// TEST 2 -- COMPLETED + BLOCKED must be a retryable contract rejection.
func TestClosureCompletedWithBlockedItemsIsContractRejected(t *testing.T) {
	fixture := newClosureFixture(t)
	fixture.harness.bodies[PurposeCEOClosure] = closureCompletedWithBlockedItems
	fixture.drive(t, 12)

	closureTask := findClosureTask(t, fixture.tasks, fixture.rootRecord(t).CorrelationID)
	if closureTask.Status == "completed" {
		t.Fatal("a completed+blocked_items closure must not be recorded as a completed task")
	}
	if closureTask.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("closure task reason_code=%q, want model_result_contract_rejected", closureTask.ReasonCode)
	}
	if !strings.Contains(closureTask.Reason, "incompatible with non-empty blocked_items") {
		t.Fatalf("closure task reason=%q must carry the precise contradiction detail", closureTask.Reason)
	}
}

// TEST 3 -- COMPLETED + UNRESOLVED must be a retryable contract rejection.
func TestClosureCompletedWithUnresolvedDecisionsIsContractRejected(t *testing.T) {
	fixture := newClosureFixture(t)
	fixture.harness.bodies[PurposeCEOClosure] = closureCompletedWithUnresolvedDecisions
	fixture.drive(t, 12)

	closureTask := findClosureTask(t, fixture.tasks, fixture.rootRecord(t).CorrelationID)
	if closureTask.Status == "completed" {
		t.Fatal("a completed+unresolved_decisions closure must not be recorded as a completed task")
	}
	if closureTask.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("closure task reason_code=%q, want model_result_contract_rejected", closureTask.ReasonCode)
	}
	if !strings.Contains(closureTask.Reason, "incompatible with non-empty") {
		t.Fatalf("closure task reason=%q must carry the precise contradiction detail", closureTask.Reason)
	}
}

// TEST 4 -- COMPLETED CLEAN must still be accepted (no regression).
func TestClosureCompletedCleanIsAccepted(t *testing.T) {
	fixture := newClosureFixture(t)
	// fixture's default PurposeCEOClosure body is already closureCompletedClean.
	fixture.drive(t, 12)

	closureTask := findClosureTask(t, fixture.tasks, fixture.rootRecord(t).CorrelationID)
	if closureTask.Status != "completed" {
		t.Fatalf("closure task status=%q, want completed", closureTask.Status)
	}
	root := fixture.rootRecord(t)
	if root.Status != "completed" {
		t.Fatalf("root status=%q, want completed (clean closure must reach the final guardrail and pass it)", root.Status)
	}
}

// TEST 5 -- NON-COMPLETED with legitimate blockers stays valid at the
// attempt level (the new check is gated on ClosureCompleted only), and is
// then handled by the existing, unmodified final guardrail.
func TestClosureNonCompletedWithBlockersStaysValidAtAttemptLevel(t *testing.T) {
	fixture := newClosureFixture(t)
	fixture.harness.bodies[PurposeCEOClosure] = closureBlockedWithBlockedItems
	fixture.drive(t, 12)

	closureTask := findClosureTask(t, fixture.tasks, fixture.rootRecord(t).CorrelationID)
	if closureTask.Status != "completed" {
		t.Fatalf("closure task status=%q, want completed -- a non-completed status with real blockers is a VALID attempt result, not a contract violation", closureTask.Status)
	}
	if closureTask.ReasonCode == "model_result_contract_rejected" {
		t.Fatal("a legitimately non-completed closure with real blockers must never be rejected as a contract violation")
	}
	root := fixture.rootRecord(t)
	if root.Status != "blocked" || root.ReasonCode != "executive_closure_not_complete" {
		t.Fatalf("root status=%q reason=%q, want blocked/executive_closure_not_complete via the UNCHANGED final guardrail", root.Status, root.ReasonCode)
	}
}

// TEST 7 -- FINAL GUARDRAIL SIGN OF LIFE: even a closure task that reaches
// "completed" with a contradictory shape -- bypassing FIX B entirely, exactly
// as an old durable row (written before this fix existed) would -- must
// still never let the root project as completed. This exercises
// orchestrator.go's "closure already completed" branch in isolation, proving
// it independently defends the root even without FIX B's attempt-time check
// ever running. The branch itself, and its blockRoot call, are byte-for-byte
// unmodified by this patch.
func TestFinalGuardrailStillRejectsContradictoryCompletedClosure(t *testing.T) {
	fixture := newClosureFixture(t)
	// Drive only up through department review, so the closure task exists
	// (created) but has not yet been driven through any attempt.
	for i := 0; i < 12; i++ {
		fixture.orchestrator.Resume(context.Background(), fixture.root) //nolint:errcheck
		root := fixture.rootRecord(t)
		all, err := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, task := range all {
			if task.TaskClass == TaskClassCoordinationCEOClosure {
				found = true
			}
		}
		if found {
			break
		}
	}
	closureTask := findClosureTask(t, fixture.tasks, fixture.rootRecord(t).CorrelationID)

	// Fabricate what an already-completed, contradictory closure attempt
	// would look like durably -- bypassing driveTypedTask's validate
	// callback (and therefore FIX B) entirely, the same shape a pre-fix
	// durable row would already have on disk.
	contradictoryResult := InvocationResult{
		InvocationID: 9001,
		JSONOutput:   json.RawMessage(closureCompletedWithBlockedItems),
	}
	fixture.harness.models.setInvocations(closureTask.ID, 1, InvocationRecord{
		ID: 9001, TaskID: closureTask.ID, AttemptID: 1, Status: "succeeded", Purpose: string(PurposeCEOClosure),
	})
	fixture.harness.models.setResult(9001, contradictoryResult)
	fixture.tasks.mu.Lock()
	fabricated := fixture.tasks.tasks[closureTask.ID]
	fabricated.Status = "completed"
	fabricated.Attempts = []AttemptRecord{{ID: 1, Ordinal: 1, State: "finished"}}
	fixture.tasks.tasks[closureTask.ID] = fabricated
	fixture.tasks.mu.Unlock()

	fixture.drive(t, 4)

	root := fixture.rootRecord(t)
	if root.Status == "completed" {
		t.Fatal("a completed closure task carrying non-empty blocked_items must never let the root project as completed")
	}
	if root.Status != "blocked" || root.ReasonCode != "executive_closure_not_complete" {
		t.Fatalf("root status=%q reason=%q, want blocked/executive_closure_not_complete", root.Status, root.ReasonCode)
	}
}

// TEST 6a -- RETRY: attempt 1 (completed+blocked, contract rejected) is
// followed by attempt 2 (corrected, clean) succeeding within the closure
// task's own retry budget -- the same shape V3 already proved live for a
// department-plan role-reference typo, now exercised for closure.
func TestClosureContradictionThenCorrectedRetrySucceeds(t *testing.T) {
	fixture := newClosureFixture(t)
	fixture.harness.bodies[PurposeCEOClosure] = closureCompletedWithBlockedItems

	// Drive until the first (rejected) closure attempt is durably recorded.
	var closureTask TaskRecord
	for i := 0; i < 12; i++ {
		fixture.orchestrator.Resume(context.Background(), fixture.root) //nolint:errcheck
		root := fixture.rootRecord(t)
		all, err := fixture.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range all {
			if task.TaskClass == TaskClassCoordinationCEOClosure {
				closureTask = task
			}
		}
		if closureTask.ID != 0 && closureTask.ReasonCode == "model_result_contract_rejected" {
			break
		}
	}
	if closureTask.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("expected the first closure attempt to be rejected, got status=%q reason=%q", closureTask.Status, closureTask.ReasonCode)
	}

	// Correct the body -- the next attempt should succeed.
	fixture.harness.bodies[PurposeCEOClosure] = closureCompletedClean
	fixture.drive(t, 12)

	root := fixture.rootRecord(t)
	if root.Status != "completed" {
		t.Fatalf("root status=%q, want completed after the corrected retry", root.Status)
	}
	finalClosure := findClosureTask(t, fixture.tasks, root.CorrelationID)
	if finalClosure.Status != "completed" {
		t.Fatalf("closure task status=%q, want completed", finalClosure.Status)
	}
}
