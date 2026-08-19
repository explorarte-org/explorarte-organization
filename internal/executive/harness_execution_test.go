package executive

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// harnessFixture is one root + one typed child task, ready to be driven.
type harnessFixture struct {
	tasks   *memoryTasks
	models  *fakeModels
	harness *fakeHarness
	budget  *countingBudget
	orch    *Orchestrator
	root    TaskRecord
	task    TaskRecord
}

func newHarnessFixture(t *testing.T, opts ...OrchestratorOption) *harnessFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	budget := &countingBudget{}
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, budget, &fakeCompletion{verdict: CompletionPass}, opts...)
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-harness",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:harness",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-harness",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	return &harnessFixture{tasks: tasksPort, models: models, harness: harness, budget: budget, orch: orchestrator, root: root, task: task}
}

func (f *harnessFixture) drive(t *testing.T) (TaskRecord, error) {
	t.Helper()
	current, err := f.tasks.GetTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f.orch.driveTypedTask(context.Background(), f.root, current, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil })
}

// TestHarnessRunCarriesTheAttemptIdentityEndToEnd is the identity trace the
// migration exists to establish: the principal that holds the lease is the
// principal the run executes as, and the worker name appears nowhere in it.
func TestHarnessRunCarriesTheAttemptIdentityEndToEnd(t *testing.T) {
	fixture := newHarnessFixture(t)
	if _, err := fixture.drive(t); err != nil {
		t.Fatal(err)
	}
	command := fixture.harness.lastCommand()
	claim := fixture.tasks.claims[0]
	if command.ExecutionPrincipalID != claim.HolderPrincipalID {
		t.Fatalf("run principal=%q lease holder=%q", command.ExecutionPrincipalID, claim.HolderPrincipalID)
	}
	if command.ExecutionPrincipalID == orchestratorWorkerID {
		t.Fatal("the run must not execute as the operational worker name")
	}
	if command.RoleID != fixture.task.AssignedRoleID {
		t.Fatalf("run role=%q want %q", command.RoleID, fixture.task.AssignedRoleID)
	}
	if command.LeaseToken == "" {
		t.Fatal("the run must carry the attempt's lease token")
	}
	if command.Context.ID <= 0 || command.Context.Content == "" || len(command.Context.Digest) != 64 {
		t.Fatalf("run context=%+v must be a rendered snapshot", command.Context)
	}
}

// TestBudgetIsCheckedBeforeTheHarnessRuns is the mutation guard for
// MaxModelCalls: removing the pre-Harness gate would let this run reach the
// provider.
func TestBudgetIsCheckedBeforeTheHarnessRuns(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.budget.err = ErrBudgetExceeded
	if _, err := fixture.drive(t); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err=%v want budget exceeded", err)
	}
	if fixture.budget.count() != 1 {
		t.Fatalf("budget consulted %d times", fixture.budget.count())
	}
	if fixture.harness.callCount() != 0 {
		t.Fatal("an exhausted budget must stop the run before the harness is entered")
	}
}

func TestBudgetIsNotChargedTwiceForOneAttempt(t *testing.T) {
	fixture := newHarnessFixture(t)
	// A run that stops without a verdict leaves the attempt running with its
	// durable invocation in place; the next tick resumes the same run.
	fixture.harness.failure = HarnessFailureAuthorityUnavailable
	if _, err := fixture.drive(t); !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("err=%v", err)
	}
	fixture.harness.failure = HarnessFailureNone
	if _, err := fixture.drive(t); err != nil {
		t.Fatal(err)
	}
	if fixture.budget.count() != 1 {
		t.Fatalf("budget charged %d times for one attempt", fixture.budget.count())
	}
}

// TestAuthorityUnavailableLeavesTheAttemptAlone: authority that could not be
// consulted is not a denial. Nothing about the attempt changes and the same
// run identity is entered again.
// An outage now schedules its own re-entry instead of leaving the attempt to
// die of lease expiry. The attempt IS recorded as failed -- but retryably,
// which is a different durable statement from the terminal one every other
// failure branch makes, and it is what puts the task back in the engine's
// hands with the engine's own backoff and attempt ceiling.
func TestAuthorityUnavailableSchedulesABoundedRetry(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureAuthorityUnavailable
	fixture.harness.invocationStatus = ""
	if _, err := fixture.drive(t); !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if len(fixture.tasks.failed) != 1 || fixture.tasks.failed[0] != "execution_authority_unavailable" {
		t.Fatalf("failure codes=%v", fixture.tasks.failed)
	}
	current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if current.Status != "retry_wait" {
		t.Fatalf("task status=%q want retry_wait -- the engine owns re-entry", current.Status)
	}
	// Still not a root-blocking condition: an outage is infrastructure, not a
	// statement about the principal.
	if !isNonBlockingPhaseError(ErrExecutionAuthorityUnavailable) {
		t.Fatal("an unavailable authority must not block the root")
	}

	// Re-entry opens a NEW attempt, and the run identity is a pure function
	// of the attempt, so the retry is a fresh trajectory rather than a resume
	// of the old one. That is correct here and only here: an authority outage
	// leaves no invocation and no history behind, so there is nothing to
	// resume and nothing a fresh run can duplicate.
	if err := fixture.tasks.Reconcile(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.drive(t); !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("resume err=%v", err)
	}
	if fixture.harness.callCount() != 2 {
		t.Fatalf("harness runs=%d want the work entered again", fixture.harness.callCount())
	}
	first, second := fixture.harness.commands[0], fixture.harness.commands[1]
	if first.RunID == second.RunID {
		t.Fatal("a new attempt reused the previous run identity")
	}
	if first.TaskID != second.TaskID {
		t.Fatalf("the retry moved to another task: %d -> %d", first.TaskID, second.TaskID)
	}
}

// The retry is bounded by the task's own max_attempts. An outage that never
// clears ends terminal instead of spinning forever.
func TestAuthorityOutageRetryIsBoundedByMaxAttempts(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureAuthorityUnavailable
	fixture.harness.invocationStatus = ""
	task, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	ceiling := task.MaxAttempts
	if ceiling < 1 {
		t.Fatalf("fixture has no attempt ceiling: %d", ceiling)
	}
	for i := 0; i < ceiling+3; i++ {
		if _, err := fixture.drive(t); err == nil {
			break
		}
		if err := fixture.tasks.Reconcile(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
		if current.Status == "failed" {
			break
		}
	}
	current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if current.Status != "failed" {
		t.Fatalf("status=%q -- an outage that never clears must stop retrying", current.Status)
	}
	if fixture.harness.callCount() > ceiling+1 {
		t.Fatalf("harness entered %d times for a ceiling of %d", fixture.harness.callCount(), ceiling)
	}
}

// The guard that matters: if the attempt somehow already carries a durable
// invocation, the outage report and the invocation record disagree, and
// retrying would risk a second provider call. That case fails closed and
// non-retryably instead.
func TestAuthorityOutageWithADurableInvocationDoesNotRetry(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureAuthorityUnavailable
	fixture.harness.invocationStatus = "succeeded"
	if _, err := fixture.drive(t); !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("err=%v", err)
	}
	current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if current.Status == "retry_wait" {
		t.Fatal("an attempt with a durable invocation was scheduled for retry")
	}
	found := false
	for _, code := range fixture.tasks.failed {
		if code == "authority_unavailable_with_durable_invocation" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failure codes=%v -- the disagreement must be recorded explicitly", fixture.tasks.failed)
	}
}

func TestAuthorizationDeniedFailsTheAttemptClosed(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureAuthorizationDenied
	fixture.harness.invocationStatus = ""
	if _, err := fixture.drive(t); !errors.Is(err, ErrExecutionAuthorityDenied) {
		t.Fatalf("err=%v", err)
	}
	if len(fixture.tasks.failed) != 1 || fixture.tasks.failed[0] != "execution_authority_denied" {
		t.Fatalf("attempt failures=%v", fixture.tasks.failed)
	}
	if isNonBlockingPhaseError(ErrExecutionAuthorityDenied) {
		t.Fatal("a denial must reach the root as a blocking phase error")
	}
}

// TestIndeterminateToolExecutionIsTerminalAndNeverRerun is the duplicate
// side-effect guard: a tool that may already have run outside the system is
// never re-entered automatically, and the root stays blocked afterwards.
func TestIndeterminateToolExecutionIsTerminalAndNeverRerun(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureIndeterminateTool
	fixture.harness.invocationStatus = ""
	if _, err := fixture.drive(t); !errors.Is(err, ErrIndeterminateToolExecution) {
		t.Fatalf("err=%v", err)
	}
	root, _ := fixture.tasks.GetTask(context.Background(), fixture.root.ID)
	if root.Status != "blocked" || root.ReasonCode != "indeterminate_tool_execution" {
		t.Fatalf("root=%+v", root)
	}
	if len(fixture.tasks.failed) != 1 || fixture.tasks.failed[0] != "indeterminate_tool_execution" {
		t.Fatalf("attempt failures=%v", fixture.tasks.failed)
	}
	if _, err := fixture.orch.Resume(context.Background(), fixture.root.ID); !errors.Is(err, ErrIndeterminateToolExecution) {
		t.Fatalf("Resume must refuse to reopen an indeterminate tool execution, got %v", err)
	}
	if fixture.harness.callCount() != 1 {
		t.Fatalf("harness runs=%d: an indeterminate tool execution was retried", fixture.harness.callCount())
	}
}

// TestToolIntentUnderAnEmptyToolSetIsNeverASuccess: Executive typed tasks
// expose no tools, so a model that asks for one fails the task rather than
// completing it or having the tool executed.
func TestToolIntentUnderAnEmptyToolSetIsNeverASuccess(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureToolRejected
	fixture.harness.invocationStatus = ""
	if _, err := fixture.drive(t); !errors.Is(err, ErrToolIntentRejected) {
		t.Fatalf("err=%v", err)
	}
	current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if current.Status == "completed" || current.Status == "awaiting_verification" {
		t.Fatalf("a rejected tool intent became status %q", current.Status)
	}
	if len(fixture.tasks.finalized) != 0 {
		t.Fatalf("a rejected tool intent finalized tasks: %v", fixture.tasks.finalized)
	}
}

func TestModelFailureFailsTheAttemptWhenNothingIsAmbiguous(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.failure = HarnessFailureModelError
	fixture.harness.invocationStatus = "failed"
	if _, err := fixture.drive(t); !errors.Is(err, ErrCompletionFailed) {
		t.Fatalf("err=%v", err)
	}
	if len(fixture.tasks.failed) != 1 || fixture.tasks.failed[0] != "model_invocation_failed" {
		t.Fatalf("attempt failures=%v", fixture.tasks.failed)
	}
}

// TestKeeperFailureOverridesHarnessSuccess is the precedence rule: the run
// says it produced an answer, the lease says it was lost while producing it,
// and the lease wins. Recording success here would write a result under an
// authority the process no longer held -- and the task engine would reject the
// write anyway, turning a clean retry into an unexplained error.
func TestKeeperFailureOverridesHarnessSuccess(t *testing.T) {
	ticker := newManualTicker()
	fixture := newHarnessFixture(t, WithLeaseKeeper(LeaseKeeperConfig{
		Interval: time.Millisecond, Extension: executiveLeaseTTL,
		NewTicker: func(time.Duration) LeaseTicker { return ticker },
	}))
	leaseLost := errors.New("lease expired while the provider was answering")
	// The lease is lost while the synchronous run is still in flight, which is
	// exactly the window the keeper exists to notice.
	fixture.harness.duringRun = func(HarnessRunCommand) {
		fixture.tasks.failHeartbeats(leaseLost)
		ticker.tick()
		waitFor(t, func() bool { return len(fixture.tasks.heartbeatActorLog()) > 0 })
	}

	_, err := fixture.drive(t)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("err=%v want the lost lease to dominate the harness verdict", err)
	}
	current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	if current.Status == "awaiting_verification" || current.Status == "completed" {
		t.Fatalf("a run whose lease was lost was recorded as status %q", current.Status)
	}
	if len(fixture.tasks.evidence) != 0 {
		t.Fatalf("evidence recorded under a lost lease: %+v", fixture.tasks.evidence)
	}
	if len(fixture.tasks.finalized) != 0 {
		t.Fatalf("task finalized under a lost lease: %v", fixture.tasks.finalized)
	}
	// The local token is dropped, so the next pass cannot adopt this attempt;
	// the task engine expires the lease and produces a fresh one.
	if _, held := fixture.orch.localLease(fixture.task.ID); held {
		t.Fatal("a lost lease must not stay in the process-local lease table")
	}
	if !isNonBlockingPhaseError(ErrLeaseLost) {
		t.Fatal("a lost lease must stay retryable instead of blocking the root")
	}
}

// TestRunIDIsDeterministicPerAttemptAndPurpose covers the identity rule the
// whole resume story rests on.
func TestRunIDIsDeterministicPerAttemptAndPurpose(t *testing.T) {
	first := harnessRunID("explorarte", 12, 44, PurposeDepartmentPlan)
	if first != "executive:explorarte:task:12:attempt:44:department-plan:v1" {
		t.Fatalf("run id=%q", first)
	}
	if second := harnessRunID("explorarte", 12, 44, PurposeDepartmentPlan); second != first {
		t.Fatalf("same attempt and purpose produced %q and %q", first, second)
	}
	if other := harnessRunID("explorarte", 12, 44, PurposeDepartmentReview); other == first {
		t.Fatal("two purposes on the same attempt must not share a run identity")
	}
	if fresh := harnessRunID("explorarte", 12, 45, PurposeDepartmentPlan); fresh == first {
		t.Fatal("a fresh attempt must produce a fresh run identity")
	}
	if crossOrg := harnessRunID("otra", 12, 44, PurposeDepartmentPlan); crossOrg == first {
		t.Fatal("run identities must be organization-scoped")
	}
	// All five purposes, on one attempt: each must be a complete enum member
	// and each must own a distinct, stable run identity. A collision here would
	// merge two different cognitive executions into one durable history.
	purposes := []ExecutionPurpose{PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentWorker, PurposeDepartmentReview, PurposeCEOClosure}
	seen := map[string]ExecutionPurpose{}
	for _, purpose := range purposes {
		if !purpose.Valid() || purpose.LegacyPurpose() == "" {
			t.Fatalf("purpose %q is not a complete enum member", purpose)
		}
		id := harnessRunID("explorarte", 12, 44, purpose)
		if want := "executive:explorarte:task:12:attempt:44:" + string(purpose) + ":v1"; id != want {
			t.Fatalf("run id=%q want %q", id, want)
		}
		if other, clash := seen[id]; clash {
			t.Fatalf("purposes %q and %q share run identity %q", other, purpose, id)
		}
		if repeat := harnessRunID("explorarte", 12, 44, purpose); repeat != id {
			t.Fatalf("purpose %q is not deterministic: %q then %q", purpose, id, repeat)
		}
		seen[id] = purpose
	}
	if len(seen) != len(purposes) {
		t.Fatalf("expected %d distinct run identities, got %d", len(purposes), len(seen))
	}
	if (ExecutionPurpose("department_plan")).Valid() {
		t.Fatal("the legacy free-text purpose must not be a valid run purpose")
	}
}

// TestUnknownPurposeNeverReachesTheHarness keeps the run-identity enum closed.
func TestUnknownPurposeNeverReachesTheHarness(t *testing.T) {
	fixture := newHarnessFixture(t)
	current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	_, err := fixture.orch.driveTypedTask(context.Background(), fixture.root, current, departmentPlanOutputSchema,
		ExecutionPurpose("something-new"), func(InvocationResult) error { return nil })
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("err=%v", err)
	}
	if fixture.harness.callCount() != 0 {
		t.Fatal("an unknown purpose must not produce a run")
	}
}

// TestHarnessSuccessMustMatchTheDurableResult: the verdict carries the answer
// for convenience, but the bytes that count are the ones Model Runtime
// persisted and hashed. A disagreement means something rewrote the answer in
// between, and neither copy is trustworthy.
func TestHarnessSuccessMustMatchTheDurableResult(t *testing.T) {
	fixture := newHarnessFixture(t)
	fixture.harness.duringRun = func(command HarnessRunCommand) {
		invocations, _ := fixture.models.FindTaskAttemptInvocations(context.Background(), command.TaskID, command.AttemptID)
		if len(invocations) != 1 {
			t.Fatalf("invocations=%d", len(invocations))
		}
		result, _ := fixture.models.GetResult(context.Background(), invocations[0].ID)
		result.JSONOutput = []byte(`{"schema_version":"department-plan/v1","department_id":"otro","tasks":[],"review_criteria":[],"unresolved":[]}`)
		fixture.models.setResult(invocations[0].ID, result)
	}
	if _, err := fixture.drive(t); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("err=%v want a rejected contract", err)
	}
	if len(fixture.tasks.evidence) != 0 {
		t.Fatalf("evidence recorded for a mismatched result: %+v", fixture.tasks.evidence)
	}
}

// TestEvidencePointsAtTheDurableInvocation keeps the evidence trail on the
// same row it always referenced.
func TestEvidencePointsAtTheDurableInvocation(t *testing.T) {
	fixture := newHarnessFixture(t)
	if _, err := fixture.drive(t); err != nil {
		t.Fatal(err)
	}
	if len(fixture.tasks.evidence) != 1 {
		t.Fatalf("evidence=%+v", fixture.tasks.evidence)
	}
	command := fixture.harness.lastCommand()
	invocations, _ := fixture.models.FindTaskAttemptInvocations(context.Background(), command.TaskID, command.AttemptID)
	want := fmt.Sprintf("model-invocation:%d", invocations[0].ID)
	if fixture.tasks.evidence[0].Reference != want {
		t.Fatalf("evidence reference=%q want %q", fixture.tasks.evidence[0].Reference, want)
	}
	if fixture.tasks.evidence[0].RecordedBy != orchestratorWorkerID {
		t.Fatalf("evidence recorded_by=%q: provenance stays the operational worker", fixture.tasks.evidence[0].RecordedBy)
	}
}
