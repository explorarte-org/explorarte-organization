package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Root 618 regression shapes (CEO_PLAN_VISIBLE_CONSTRAINTS_AND_OWNER_DECISION_LIFECYCLE_FIX_V1).
//
// Three real production attempts against a code-runner-bounded campaign were
// all recorded as model_result_contract_rejected/retryable:
//
//   - Attempt 1 and 2: a structurally VALID ExecutivePlan that named an
//     owner_decisions_required entry -- not a contract defect, but the
//     lifecycle check at the time folded it into the same rejection path as
//     a malformed plan.
//   - Attempt 3: an ACTUALLY invalid plan (two department_requests) against a
//     rule -- exactly one department, unit_id "ingenieria_ia" -- that the CEO
//     was never shown before generating.
//
// The three fixtures below reproduce each shape against the real
// Orchestrator (in-memory ports, no network, no Postgres) and pin the
// corrected behavior: CASE A blocks the root for the owner after exactly one
// provider call and is never autonomously reconsidered; CASE B stays a
// retryable contract rejection carrying its precise detail; CASE C is
// untouched and advances the campaign.

// codeRunnerFixture is a root carrying CodeRunnerExecutionEvidenceRequirementKey
// -- root 618's actual shape -- driven by a scripted CEO plan body.
type codeRunnerFixture struct {
	orchestrator *Orchestrator
	tasks        *memoryTasks
	harness      *scriptedHarness
	root         int64
}

func newCodeRunnerFixture(t *testing.T, ceoPlanBody string) *codeRunnerFixture {
	t.Helper()
	return buildCodeRunnerFixture(t, ceoPlanBody, true)
}

// newNormalCampaignFixture is identical to newCodeRunnerFixture except the
// root never carries CodeRunnerExecutionEvidenceRequirementKey -- an
// ordinary campaign, used to pin that it receives no code-runner-specific
// guidance at either the CEO-plan or department-plan layer.
func newNormalCampaignFixture(t *testing.T, ceoPlanBody string) *codeRunnerFixture {
	t.Helper()
	return buildCodeRunnerFixture(t, ceoPlanBody, false)
}

func buildCodeRunnerFixture(t *testing.T, ceoPlanBody string, requireCodeRunner bool) *codeRunnerFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	acceptance := newMemoryAcceptance()
	models := newFakeModels()
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: map[ExecutionPurpose]string{
		PurposeCEOPlan: ceoPlanBody,
		PurposeDepartmentPlan: `{"schema_version":"department-plan/v1","department_id":"ingenieria_ia",` +
			`"tasks":[{"client_key":"audit-1","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.design",` +
			`"title":"Run the code-runner audit","instructions":"Execute the bounded audit.",` +
			`"acceptance_criteria":["audit evidence recorded"],"dependencies":[],"requirements":[],"priority":50}],` +
			`"review_criteria":["audit evidence recorded"],"unresolved":[]}`,
	}}

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	worker := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	servicios := RoleRef{ID: "servicios/orquestador", UnitID: "servicios", Enabled: true, Executable: true, CanonicalLeader: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	registry := fakeRegistry{
		// memoryTasks.CreateTask hardcodes OrganizationRevisionID=7 on every
		// task it creates (see test_fakes_test.go); the revision here must
		// match that fixed value or every Resume call blocks immediately on
		// organization_revision_drift before the CEO plan is ever reached.
		rev: RevisionRef{ID: 7},
		units: map[string]UnitRef{
			"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID},
			"servicios":     {ID: "servicios", Operational: true, LeaderRoleID: servicios.ID},
		},
		roles:   map[string]RoleRef{leader.ID: leader, worker.ID: worker, servicios.ID: servicios, ceo.ID: ceo},
		leaders: map[string]RoleRef{"ingenieria_ia": leader, "servicios": servicios},
	}
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: acceptance,
		OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort, Contexts: &fakeContexts{},
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(618, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	requirements := []RequirementProposal{}
	if requireCodeRunner {
		requirements = append(requirements, RequirementProposal{
			Key: CodeRunnerExecutionEvidenceRequirementKey, Type: "result",
			Description: "real CodeRunner evidence with all host gates", Required: true,
		})
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "root618-fixture-" + t.Name(),
		Goal: OwnerGoal{
			Goal: "CICLO AUTONOMO DIARIO DE MEJORA Y AUDITORIA CONTINUA",
			AcceptanceCriteria: []AcceptanceCriterion{
				{Text: "the daily cycle design is frozen", Phase: AcceptanceDesign},
				{Text: "code-runner audit evidence recorded", Phase: AcceptanceImplementation},
			},
			Requirements: requirements,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &codeRunnerFixture{orchestrator: orchestrator, tasks: tasksPort, harness: harness, root: run.RootTaskID}
}

func (f *codeRunnerFixture) drive(t *testing.T, maxPasses int) Run {
	t.Helper()
	var last Run
	for i := 0; i < maxPasses; i++ {
		run, err := f.orchestrator.Resume(context.Background(), f.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrTaskRetryScheduled) && !errors.Is(err, ErrModelResultContractRejected) {
			t.Fatalf("resume %d: %v", i, err)
		}
		last = run
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	return last
}

func (f *codeRunnerFixture) rootRecord(t *testing.T) TaskRecord {
	t.Helper()
	root, err := f.tasks.GetTask(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

const (
	ceoPlanCaseAGenuineOwnerDecision = `{"schema_version":"executive-plan/v1","objective":"Daily improvement cycle audit",` +
		`"department_requests":[{"unit_id":"ingenieria_ia","objective":"run code-runner audit","deliverable":"audit evidence","priority":1,"constraints":[]}],` +
		`"global_constraints":[],"success_criteria":["audit evidence recorded"],` +
		`"owner_decisions_required":["Adjudicate freeze, revise, or reject for the daily cycle design before it may run unattended."]}`

	ceoPlanCaseBTwoDepartments = `{"schema_version":"executive-plan/v1","objective":"Daily improvement cycle audit",` +
		`"department_requests":[` +
		`{"unit_id":"ingenieria_ia","objective":"run code-runner audit","deliverable":"audit evidence","priority":1,"constraints":[]},` +
		`{"unit_id":"servicios","objective":"write operability runbook","deliverable":"runbook","priority":2,"constraints":[]}` +
		`],"global_constraints":[],"success_criteria":["audit evidence recorded"],"owner_decisions_required":[]}`

	ceoPlanCaseCCleanSingleDepartment = `{"schema_version":"executive-plan/v1","objective":"Daily improvement cycle audit",` +
		`"department_requests":[{"unit_id":"ingenieria_ia","objective":"run code-runner audit","deliverable":"audit evidence","priority":1,"constraints":[]}],` +
		`"global_constraints":[],"success_criteria":["audit evidence recorded"],"owner_decisions_required":[]}`
)

// ROOT618_CASE_A: a structurally valid plan that names a genuine owner
// decision must block the root for the owner after exactly ONE provider
// call -- not be recorded as a retryable contract rejection, and not be
// autonomously reconsidered.
func TestRoot618CaseA_ValidPlanWithOwnerDecisionBlocksNotRejects(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseAGenuineOwnerDecision)
	fixture.drive(t, 8)

	root := fixture.rootRecord(t)
	if root.Status != "blocked" {
		t.Fatalf("root.Status=%q, want blocked", root.Status)
	}
	if root.ReasonCode != ReasonOwnerDecisionRequired {
		t.Fatalf("root.ReasonCode=%q, want %q", root.ReasonCode, ReasonOwnerDecisionRequired)
	}
	if !strings.Contains(root.Reason, "model-invocation:") {
		t.Fatalf("block reason must point at the durable model result, got %q", root.Reason)
	}
	if root.ReasonCode == "model_result_contract_rejected" {
		t.Fatal("a valid plan with an owner decision must never be recorded as a contract rejection")
	}
	if IsAutonomouslyReconsiderableBlockedReason(root.ReasonCode) {
		t.Fatal("owner_decision_required must never be autonomously reconsiderable")
	}
	if got := len(fixture.harness.commands); got != 1 {
		t.Fatalf("provider calls before block = %d, want 1", got)
	}
	for _, task := range mustListByCorrelation(t, fixture.tasks, root.CorrelationID) {
		if task.AssignedUnitID == "ingenieria_ia" && task.ID != root.ID && strings.Contains(task.Title, "Department planning") {
			t.Fatalf("no department task may be created while an owner decision is pending, found %q", task.Title)
		}
	}
}

// ROOT618_CASE_B: an actually invalid code-runner plan (two departments)
// must stay a retryable model_result_contract_rejected, carrying the precise
// violated-rule detail -- this is the genuine contract-rejection path and
// must not be confused with CASE A.
func TestRoot618CaseB_TwoDepartmentsIsPreciseContractRejection(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseBTwoDepartments)
	run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
	if err == nil {
		t.Fatal("a two-department code-runner plan must be rejected, got nil error")
	}
	if !errors.Is(err, ErrModelResultContractRejected) && !errors.Is(err, ErrTaskRetryScheduled) {
		t.Fatalf("err=%v, want a contract-rejection/retry-scheduled error", err)
	}
	if !strings.Contains(err.Error(), "exactly one department, got 2") {
		t.Fatalf("err=%v must carry the precise code-runner detail", err)
	}
	root := fixture.rootRecord(t)
	if root.Status == "blocked" {
		t.Fatalf("a retryable contract rejection must not block the root, got blocked: %s", root.Reason)
	}
	_ = run
	// The in-memory tasks fake does not populate TaskRecord.Attempts (only
	// the real tasks.Service does; see internal/tasks/contextprovider's
	// TestMeasuredContractRejectionReachesTheNextAttemptContext for that
	// transport proven against real Attempt records). What the fake DOES
	// carry, identically to the real engine's RecordAttemptFailed, is the
	// durable code/reason pair on the task itself.
	planTask := findCEOPlanTask(t, fixture.tasks, root.CorrelationID)
	if planTask.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("plan task reason_code=%q, want model_result_contract_rejected", planTask.ReasonCode)
	}
	if !strings.Contains(planTask.Reason, "exactly one department, got 2") {
		t.Fatalf("plan task reason=%q must carry the precise detail", planTask.Reason)
	}
}

// ROOT618_CASE_C: a clean single-department, no-owner-decision plan is
// unaffected by this fix -- the CEO plan task completes and the campaign
// advances to department planning.
func TestRoot618CaseC_CleanPlanAdvancesToDepartmentPlanning(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseCCleanSingleDepartment)
	// This case only asserts that the campaign ADVANCES past the CEO plan --
	// not that it fully closes, which would require scripting every later
	// purpose (department worker, review, CEO closure) irrelevant to this
	// fix. Errors from those later, unscripted phases are expected once the
	// department-planning task itself is confirmed and are not fatal here.
	var planTask TaskRecord
	var leaderTaskFound bool
	for i := 0; i < 6; i++ {
		if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil &&
			!errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrTaskRetryScheduled) &&
			!errors.Is(err, ErrModelResultContractRejected) && !errors.Is(err, ErrCompletionFailed) {
			t.Fatalf("resume %d: %v", i, err)
		}
		root := fixture.rootRecord(t)
		if root.ReasonCode == ReasonOwnerDecisionRequired {
			t.Fatal("a plan with no owner decisions must never block as owner_decision_required")
		}
		planTask = findCEOPlanTask(t, fixture.tasks, root.CorrelationID)
		for _, task := range mustListByCorrelation(t, fixture.tasks, root.CorrelationID) {
			if strings.Contains(task.Title, "Department planning: ingenieria_ia") {
				leaderTaskFound = true
			}
		}
		if planTask.Status == "completed" && leaderTaskFound {
			break
		}
	}
	if planTask.Status != "completed" {
		t.Fatalf("CEO plan task status=%q, want completed", planTask.Status)
	}
	if !leaderTaskFound {
		t.Fatal("a clean plan must advance the campaign into department planning")
	}
}

// CODE_RUNNER_CONSTRAINT_VISIBLE_TO_CEO: the same requirement that gates
// validateCodeRunnerExecutivePlan must also make the constraint text reach
// the provider-visible ExecutionContract before the CEO ever answers.
func TestCodeRunnerConstraintIsVisibleToTheCEOBeforeGeneration(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseCCleanSingleDepartment)
	fixture.orchestrator.Resume(context.Background(), fixture.root) //nolint:errcheck
	command := fixture.harness.commands[0]
	if command.Purpose != PurposeCEOPlan {
		t.Fatalf("first command purpose=%q, want ceo-plan", command.Purpose)
	}
	if !strings.Contains(command.ExecutionContract, "CODE_RUNNER_EXECUTIVE_PLAN_CONSTRAINTS") {
		t.Fatalf("ExecutionContract=%q must carry the code-runner constraint guidance", command.ExecutionContract)
	}
	if !strings.Contains(command.ExecutionContract, "ingenieria_ia") {
		t.Fatal("ExecutionContract must name the required unit_id")
	}
}

// NORMAL_CAMPAIGN_RECEIVES_CODE_RUNNER_CONSTRAINT = NO: a root without
// CodeRunnerExecutionEvidenceRequirementKey must never receive audit-only
// guidance, for either purpose the contract covers -- and a purpose the
// contract has nothing to say about must stay silent even for a root that
// DOES carry the requirement.
func TestNormalCampaignDoesNotReceiveCodeRunnerConstraint(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{PurposeCEOPlan, PurposeDepartmentPlan} {
		if got := codeRunnerExecutionConstraintGuidance(TaskRecord{}, purpose); got != "" {
			t.Fatalf("purpose=%q: a root with no requirements must get no guidance, got %q", purpose, got)
		}
		root := TaskRecord{Requirements: []RequirementRecord{{Key: "some-other-requirement", Required: true}}}
		if got := codeRunnerExecutionConstraintGuidance(root, purpose); got != "" {
			t.Fatalf("purpose=%q: a root without the code-runner requirement must get no guidance, got %q", purpose, got)
		}
	}

	codeRunnerRoot := TaskRecord{Requirements: []RequirementRecord{
		{Key: CodeRunnerExecutionEvidenceRequirementKey, Required: true},
	}}
	if got := codeRunnerExecutionConstraintGuidance(codeRunnerRoot, PurposeDepartmentReview); got != "" {
		t.Fatalf("a purpose the contract has no guidance for must stay silent even on a code-runner root, got %q", got)
	}
}

func mustListByCorrelation(t *testing.T, tasksPort *memoryTasks, correlation string) []TaskRecord {
	t.Helper()
	values, err := tasksPort.ListByCorrelation(context.Background(), correlation)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func findCEOPlanTask(t *testing.T, tasksPort *memoryTasks, correlation string) TaskRecord {
	t.Helper()
	// The ROOT task also carries AssignedRoleID==CEORoleID (it is the owner
	// goal, assigned to the CEO for planning) -- TaskClass is what actually
	// distinguishes the CEO-plan CHILD task from its root.
	for _, task := range mustListByCorrelation(t, tasksPort, correlation) {
		if task.TaskClass == TaskClassCoordinationCEOPlan {
			return task
		}
	}
	t.Fatal("no CEO plan task found")
	return TaskRecord{}
}

func findDepartmentPlanTask(t *testing.T, tasksPort *memoryTasks, correlation string) TaskRecord {
	t.Helper()
	for _, task := range mustListByCorrelation(t, tasksPort, correlation) {
		if task.TaskClass == TaskClassCoordinationDeptPlan {
			return task
		}
	}
	t.Fatal("no department plan task found")
	return TaskRecord{}
}

// CODE_RUNNER_DEPARTMENT_PLAN_VISIBLE_CONSTRAINT_FIX_V1.
//
// validateCodeRunnerDepartmentPlan enforces departmentID=="ingenieria_ia"
// and exactly one task, but -- before this fix -- nothing projected "exactly
// ONE task" to the model producing DepartmentPlan, the same shape of gap
// PR #204 closed for the CEO-plan layer. The tests below reproduce and pin
// the department-plan half.

const departmentPlanCaseTwoTasks = `{"schema_version":"department-plan/v1","department_id":"ingenieria_ia",` +
	`"tasks":[` +
	`{"client_key":"audit-1","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.design",` +
	`"title":"Run the code-runner audit","instructions":"Execute the bounded audit.",` +
	`"acceptance_criteria":["audit evidence recorded"],"dependencies":[],"requirements":[],"priority":50},` +
	`{"client_key":"audit-2","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.design",` +
	`"title":"A second, parallel task","instructions":"Do more in parallel.",` +
	`"acceptance_criteria":["audit evidence recorded"],"dependencies":[],"requirements":[],"priority":50}` +
	`],"review_criteria":["audit evidence recorded"],"unresolved":[]}`

// PROVE_THE_GAP: before this fix, PurposeDepartmentPlan's ExecutionContract
// carried no code-runner-specific text at all, even though
// validateCodeRunnerDepartmentPlan was already enforcing "exactly one task"
// host-side. This is the provider-visible assertion the round requires --
// not a helper-string test -- inspecting the real HarnessRunCommand the
// department leader would receive.
func TestCodeRunnerDepartmentPlanConstraintIsVisibleBeforeGeneration(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseCCleanSingleDepartment)
	for i := 0; i < 4; i++ {
		fixture.orchestrator.Resume(context.Background(), fixture.root) //nolint:errcheck
	}
	var command HarnessRunCommand
	found := false
	for _, c := range fixture.harness.commands {
		if c.Purpose == PurposeDepartmentPlan {
			command = c
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no PurposeDepartmentPlan command was ever issued")
	}
	if !strings.Contains(command.ExecutionContract, "CODE_RUNNER_DEPARTMENT_PLAN_CONSTRAINTS") {
		t.Fatalf("ExecutionContract=%q must carry the code-runner department-plan guidance", command.ExecutionContract)
	}
	if !strings.Contains(command.ExecutionContract, "exactly ONE task") {
		t.Fatalf("ExecutionContract=%q must state the exactly-one-task cardinality", command.ExecutionContract)
	}
	if !strings.Contains(command.ExecutionContract, "ingenieria_ia") {
		t.Fatal("ExecutionContract must name the required department")
	}
}

// DEPARTMENT_PLAN / CodeRunner, invalid shape: two tasks must be a precise,
// retryable contract rejection -- the department-plan mirror of ROOT618_CASE_B.
func TestDepartmentPlanCaseTwoTasksIsPreciseContractRejection(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseCCleanSingleDepartment)
	fixture.harness.bodies[PurposeDepartmentPlan] = departmentPlanCaseTwoTasks

	var lastErr error
	for i := 0; i < 4; i++ {
		_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil {
			lastErr = err
		}
		root := fixture.rootRecord(t)
		if root.Status == "blocked" {
			t.Fatalf("a retryable contract rejection must not block the root, got blocked: %s", root.Reason)
		}
		deptTask := findDepartmentPlanTask(t, fixture.tasks, root.CorrelationID)
		if deptTask.ReasonCode == "model_result_contract_rejected" {
			if !strings.Contains(deptTask.Reason, "exactly one department task, got 2") {
				t.Fatalf("department plan task reason=%q must carry the precise detail", deptTask.Reason)
			}
			return
		}
	}
	t.Fatalf("a two-task code-runner department plan must be rejected; last err=%v", lastErr)
}

// ROOT618_SUCCESSOR_DEPARTMENT_CASE: extends CASE C past "the department
// planning task exists" into "the department leader actually saw the
// constraint and produced a plan the host validated" -- the campaign
// advances beyond department planning. Closing the whole campaign is out of
// scope; reaching a completed department-plan task is the advancement this
// fix is responsible for.
func TestRoot618SuccessorDepartmentCase_CleanDepartmentPlanAdvancesCampaign(t *testing.T) {
	fixture := newCodeRunnerFixture(t, ceoPlanCaseCCleanSingleDepartment)
	// departmentPlanCaseCleanSingleTask: the fixture's default scripted body
	// already matches this shape (one task, ingenieria_ia/qa), see
	// newCodeRunnerFixture/buildCodeRunnerFixture above.
	var deptTask TaskRecord
	for i := 0; i < 6; i++ {
		if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil &&
			!errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrTaskRetryScheduled) &&
			!errors.Is(err, ErrModelResultContractRejected) && !errors.Is(err, ErrCompletionFailed) {
			t.Fatalf("resume %d: %v", i, err)
		}
		root := fixture.rootRecord(t)
		if root.ReasonCode == ReasonOwnerDecisionRequired {
			t.Fatal("a clean plan must never block as owner_decision_required")
		}
		all := mustListByCorrelation(t, fixture.tasks, root.CorrelationID)
		hasDeptPlanTask := false
		for _, task := range all {
			if task.TaskClass == TaskClassCoordinationDeptPlan {
				hasDeptPlanTask = true
			}
		}
		if !hasDeptPlanTask {
			continue
		}
		deptTask = findDepartmentPlanTask(t, fixture.tasks, root.CorrelationID)
		if deptTask.Status == "completed" {
			break
		}
	}
	if deptTask.Status != "completed" {
		t.Fatalf("department plan task status=%q, want completed -- a clean, constraint-compliant plan must validate and advance", deptTask.Status)
	}
}

// NORMAL_DEPARTMENT_CAMPAIGN_UNCHANGED / NORMAL_CEO_CAMPAIGN_UNCHANGED,
// provider-visible: a root that never opted into the code-runner audit must
// never see CODE_RUNNER guidance in EITHER purpose's real ExecutionContract.
func TestNormalCampaignExecutionContractNeverCarriesCodeRunnerGuidance(t *testing.T) {
	fixture := newNormalCampaignFixture(t, ceoPlanCaseCCleanSingleDepartment)
	for i := 0; i < 4; i++ {
		fixture.orchestrator.Resume(context.Background(), fixture.root) //nolint:errcheck
	}
	sawCEOPlan, sawDepartmentPlan := false, false
	for _, command := range fixture.harness.commands {
		if strings.Contains(command.ExecutionContract, "CODE_RUNNER") {
			t.Fatalf("purpose=%q: a normal campaign must never receive code-runner guidance, got %q", command.Purpose, command.ExecutionContract)
		}
		if command.Purpose == PurposeCEOPlan {
			sawCEOPlan = true
		}
		if command.Purpose == PurposeDepartmentPlan {
			sawDepartmentPlan = true
		}
	}
	if !sawCEOPlan || !sawDepartmentPlan {
		t.Fatalf("test did not actually exercise both purposes: ceo=%v department=%v", sawCEOPlan, sawDepartmentPlan)
	}
}
