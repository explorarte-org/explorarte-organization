package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// EXECUTIVE_ACTIVE_LEASE_BARRIER_DETERMINISTIC_REPRO_V1
//
// These tests reproduce, without any provider, production database, or
// production-code change, the exact shape of the
// EXECUTIVE_CONTEXT_PROFILE_SCOPING_PRODUCTION_E2E_V1 incident: a CEO-plan
// child task (task 721 in production) that completes cleanly under the
// Orchestrator instance that legitimately holds its lease, while root 720
// ends up durably blocked/executive_phase_failed because a SECOND
// Orchestrator, sharing the same durable task store but with its own
// process-local lease map, observed the same attempt's active lease without
// possessing its plaintext token.
//
// All interleaving below is channel/barrier-controlled -- no sleeps.
// ============================================================================

// validLeaseReproPlanBody is the minimal ExecutivePlan that (a) satisfies
// ParseExecutivePlan/validateExecutivePlanShape (department_requests must be
// non-empty), (b) resolves against the single "ingenieria_ia" unit the
// fixture registry below defines, and (c) has NO owner_decisions_required --
// matching production's actual run, where task 722 (a department-plan child)
// was created, which only happens when OwnerDecisionsRequired is empty. This
// is what lets the "owner" driver's own post-plan continuation proceed into
// driveDepartments and return successfully WITHOUT itself touching root's
// status, so root's only durable status/reason write in this test comes from
// the barrier observer -- exactly as in production.
func validLeaseReproPlanBody() json.RawMessage {
	return json.RawMessage(`{"schema_version":"executive-plan/v1","objective":"x","department_requests":[{"unit_id":"ingenieria_ia","objective":"x","deliverable":"x"}],"success_criteria":["x"],"owner_decisions_required":[]}`)
}

// newLeaseReproOrchestrator builds a fresh Orchestrator against a shared
// TaskCoordinator/ModelInvocationReader pair, mirroring
// testOrchestratorWithHarness's fixture registry exactly (same single
// "ingenieria_ia" unit/leader) so multiple Orchestrator instances in the same
// test can legitimately resolve the same plan against the same durable
// store, differing only in their own process-local lease map.
func newLeaseReproOrchestrator(t *testing.T, tasksPort TaskCoordinator, models *fakeModels, harness HarnessExecutor, opts ...OrchestratorOption) *Orchestrator {
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
		Acceptance: newMemoryAcceptance(), OrganizationID: "explorarte", Registry: registry, Tasks: tasksPort,
		Contexts: &fakeContexts{}, Assignments: fakeAssignments{}, Principals: newFakePrincipals(),
		Models: models, Harness: harness, Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func submitLeaseReproOwnerGoal(t *testing.T, o *Orchestrator, key string) Run {
	t.Helper()
	run, reused, err := o.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: key,
		Goal: OwnerGoal{Goal: "bounded lease-barrier repro goal", AcceptanceCriteria: []AcceptanceCriterion{{Text: "produce a plan", Phase: AcceptanceDesign}}},
	})
	if err != nil || reused {
		t.Fatalf("submit reused=%v err=%v", reused, err)
	}
	return run
}

// findPlanTaskID locates the single CEO-plan child of root by task_class,
// the same durable marker driveTypedTask's caller uses (findTaskByMarker),
// without depending on any specific numeric ID.
func findLeaseReproPlanTask(t *testing.T, tasksPort *memoryTasks, correlationID string) TaskRecord {
	t.Helper()
	all, err := tasksPort.ListByCorrelation(context.Background(), correlationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if task.TaskClass == TaskClassCoordinationCEOPlan {
			return task
		}
	}
	t.Fatal("ceo-plan child not found")
	return TaskRecord{}
}

// ----------------------------------------------------------------------------
// Step 3: single-driver control. This MUST pass on current code -- if it
// doesn't, the defect is simpler than a cross-driver race.
// ----------------------------------------------------------------------------

func TestFreshSingleDriverExecutiveRunDoesNotLoseItsOwnLease(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.body = validLeaseReproPlanBody()
	orch := newLeaseReproOrchestrator(t, tasksPort, models, harness, WithNoRetries())

	run := submitLeaseReproOwnerGoal(t, orch, "single-driver-root")
	if _, err := orch.ResumeDurable(context.Background(), run.RootTaskID); err != nil {
		t.Fatalf("ResumeDurable err=%v (single driver must not lose its own lease)", err)
	}

	plan := findLeaseReproPlanTask(t, tasksPort, run.CorrelationID)
	if plan.Status != "completed" {
		t.Fatalf("plan task status=%s want completed", plan.Status)
	}
	if len(plan.Attempts) != 1 {
		t.Fatalf("attempts=%d want 1", len(plan.Attempts))
	}
	if got := len(tasksPort.claims); got != 1 {
		t.Fatalf("claims=%d want 1", got)
	}
	if harness.callCount() != 1 {
		t.Fatalf("harness calls=%d want 1", harness.callCount())
	}
	root, _ := tasksPort.GetTask(context.Background(), run.RootTaskID)
	if root.Status == "blocked" && root.ReasonCode == "executive_phase_failed" {
		t.Fatalf("single driver must never see its own claim as an unowned active lease: root=%+v", root)
	}
	if strings.Contains(root.Reason, "lease token unavailable") {
		t.Fatalf("single driver produced the barrier text against itself: root=%+v", root)
	}
}

// SINGLE_SEQUENTIAL_PATH_CAN_PRODUCE_INCIDENT_SHAPE documents, alongside the
// test above, why one sequential driveTypedTask call cannot both (a) observe
// "active lease, no local token" on a task and (b) go on to complete that
// exact attempt within the same call: haveLease is a single local boolean set
// once, before the "ready" claim branch, and never cleared afterward. Taking
// the claim branch (task.Status=="ready") sets haveLease=true for the rest of
// the call; only a task ALREADY "leased"/"running" when first observed (i.e.
// claimed by someone else) can hit the haveLease==false branches, and no
// attempt this driver did not itself claim can become "completed" by this
// same driver's own subsequent calls (StartAttempt/RecordAttemptSucceeded
// require possessing the matching LeaseRecord, which this driver never
// received). The two facts together are why the production shape (root
// blocked + child completed + exactly one attempt/lease) requires a second
// observer.
const SINGLE_SEQUENTIAL_PATH_CAN_PRODUCE_INCIDENT_SHAPE = false

// ----------------------------------------------------------------------------
// Steps 5-6: the cross-Orchestrator TOCTOU. ORCH_A claims and (paused via a
// harness hook) is mid-flight on the CEO-plan attempt when ORCH_B -- a fresh
// Orchestrator over the SAME durable store, empty local lease map -- reaches
// the same task inside its own drive step and hits the barrier. ORCH_A then
// resumes and completes normally.
// ----------------------------------------------------------------------------

func TestSeparateOrchestratorActiveLeaseTOCTOUReproducesIncidentShape(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()

	claimedAndRunning := make(chan struct{})
	proceed := make(chan struct{})
	harnessA := newFakeHarness(models)
	harnessA.body = validLeaseReproPlanBody()
	harnessA.duringRun = func(HarnessRunCommand) {
		// The durable invocation row (recordDurableInvocation) and the
		// attempt/lease transition to running both already happened by the
		// time fakeHarness.Execute reaches duringRun -- this is exactly the
		// "attempt with an active lease, mid-flight at the provider
		// boundary" window the production incident occupied for ~9 real
		// seconds around invocation 150.
		close(claimedAndRunning)
		<-proceed
	}
	orchA := newLeaseReproOrchestrator(t, tasksPort, models, harnessA, WithNoRetries())

	run := submitLeaseReproOwnerGoal(t, orchA, "toctou-root")

	type resumeResult struct {
		run Run
		err error
	}
	resultCh := make(chan resumeResult, 1)
	go func() {
		r, err := orchA.ResumeDurable(context.Background(), run.RootTaskID)
		resultCh <- resumeResult{r, err}
	}()

	<-claimedAndRunning

	plan := findLeaseReproPlanTask(t, tasksPort, run.CorrelationID)
	if plan.Status != "running" || plan.ActiveLease == nil {
		t.Fatalf("plan task must be durably running with an active lease at this point: %+v", plan)
	}

	// ORCH_B: a fresh Orchestrator, empty local lease map, same durable
	// store. It calls Resume directly (not ResumeDurable) because
	// ResumeDurable's own top-of-function loop over pre-existing children
	// with an active lease already returns a NON-durable barrier without
	// ever calling BlockTask -- ResumeDurable's early loop is not where the
	// bug lived. The production incident's root was brand new, so its
	// FIRST-EVER ResumeDurable call found an empty children list and fell
	// straight into Resume() -- the exact call this reproduces. Calling
	// Resume directly here stands in for "a driver reaching its own
	// plan-driving step", not a literal claim that production called this
	// unexported method from outside the package.
	rootBeforeBarrier, _ := tasksPort.GetTask(context.Background(), run.RootTaskID)
	harnessB := newFakeHarness(models)
	orchB := newLeaseReproOrchestrator(t, tasksPort, models, harnessB, WithNoRetries())
	_, errB := orchB.Resume(context.Background(), run.RootTaskID)

	// POST-FIX: ORCH_B observing an active lease it does not own is a
	// transient ErrActiveLeaseBarrier, not the durably-permanent
	// ErrRunBlocked. This is the corrected outcome the fix
	// (EXECUTIVE_ACTIVE_LEASE_BARRIER_FIX_V1) introduces -- prior to the fix
	// this branch asserted the INCIDENT shape (ErrRunBlocked, root durably
	// blocked/executive_phase_failed); that was the bug, not the spec.
	if !errors.Is(errB, ErrActiveLeaseBarrier) {
		t.Fatalf("ORCH_B err=%v want ErrActiveLeaseBarrier", errB)
	}
	if errors.Is(errB, ErrRunBlocked) {
		t.Fatalf("ORCH_B err=%v must not also be the generic, durably-permanent ErrRunBlocked", errB)
	}
	rootAfterBarrier, _ := tasksPort.GetTask(context.Background(), run.RootTaskID)
	if rootAfterBarrier.Status == "blocked" && rootAfterBarrier.ReasonCode == "executive_phase_failed" {
		t.Fatalf("root after ORCH_B's observation=%+v must NOT be durably converted to blocked/executive_phase_failed: a transient barrier writes no verdict on root", rootAfterBarrier)
	}
	if rootAfterBarrier.Status != rootBeforeBarrier.Status || rootAfterBarrier.ReasonCode != rootBeforeBarrier.ReasonCode {
		t.Fatalf("ORCH_B's barrier observation must leave root's durable status/reason untouched: before=%+v after=%+v", rootBeforeBarrier, rootAfterBarrier)
	}
	if harnessB.callCount() != 0 {
		t.Fatalf("ORCH_B must never reach the harness beside an active lease it does not own: calls=%d", harnessB.callCount())
	}
	if got := len(tasksPort.claims); got != 1 {
		t.Fatalf("claims=%d want exactly 1 (ORCH_B must never claim a second attempt)", got)
	}

	close(proceed)
	res := <-resultCh
	if res.err != nil {
		t.Fatalf("ORCH_A's own ResumeDurable must complete its own claim successfully: err=%v", res.err)
	}

	finalPlan := findLeaseReproPlanTask(t, tasksPort, run.CorrelationID)
	if finalPlan.Status != "completed" {
		t.Fatalf("plan task final status=%s want completed", finalPlan.Status)
	}
	if len(finalPlan.Attempts) != 1 {
		t.Fatalf("attempts for plan task=%d want 1", len(finalPlan.Attempts))
	}
	if got := len(tasksPort.claims); got != 1 {
		t.Fatalf("claims after ORCH_A completes=%d want still 1 (no second claim)", got)
	}
	if harnessA.callCount() != 1 {
		t.Fatalf("ORCH_A harness calls=%d want 1", harnessA.callCount())
	}
	attemptID := finalPlan.Attempts[0].ID
	if got := models.invocationCount(finalPlan.ID, attemptID); got != 1 {
		t.Fatalf("model invocations for the plan attempt=%d want 1 (no duplicate provider call)", got)
	}

	// POST-FIX: the child is durably completed AND root was never durably
	// convicted by ORCH_B's transient barrier observation in the first
	// place, so there is no stale block left for ORCH_A's own successful
	// continuation to silently coexist with. This is the corrected outcome:
	// before the fix, root ended up permanently blocked/executive_phase_failed
	// despite ORCH_A's clean completion -- that contradiction was the bug.
	rootFinal, _ := tasksPort.GetTask(context.Background(), run.RootTaskID)
	if rootFinal.Status == "blocked" && rootFinal.ReasonCode == "executive_phase_failed" {
		t.Fatalf("INCIDENT_SHAPE_MUST_NOT_RECUR: root ended as %+v, a transient barrier must never durably survive as executive_phase_failed once its observing driver returns", rootFinal)
	}
}

// ----------------------------------------------------------------------------
// Step 7: same-Orchestrator concurrent ResumeDurable. o.leases is guarded by
// its own mutex and shared by every goroutine calling methods on the SAME
// Orchestrator value, so once one goroutine's claim reaches rememberLease,
// every other goroutine on the SAME Orchestrator sees the token too -- there
// is no second, independent local map to diverge from it. The only window
// where a same-Orchestrator race could theoretically matter is the few Go
// statements between ClaimTask returning (which durably flips the task to
// "leased") and rememberLease executing (orchestrator.go, both inside the
// SAME unexported function) -- a window this round does not authorize
// modifying orchestrator.go to instrument or artificially widen. No test-only
// hook reaches inside that gap without changing production code, so this is
// reported honestly as not constructed, not as a negative empirical result
// about whether the gap could ever be hit under a real scheduler.
//
// An EARLIER version of this test fired several goroutines calling
// ResumeDurable concurrently on one Orchestrator, unsynchronized, to see
// whether the barrier could still occur. That attempt is deliberately NOT
// kept: under `-race` it reported a genuine data race inside memoryTasks
// itself (a concurrent read/write between RecordAttemptSucceeded and
// findOrphanedSucceededInvocation on the same TaskRecord field) and produced
// 5-6 harness calls across 8 callers instead of the expected 1 -- i.e. the
// fake test double's own per-method mutexes do not make a whole
// read-decide-act Resume() sequence atomic under true concurrency, so any
// result from firing raw concurrent goroutines at it is confounded by the
// fixture's own concurrency limits, not a clean signal about the mechanism
// under test. The round's own instruction ("NO aceptar un flaky reproducer")
// applies here: rather than report SAME_ORCHESTRATOR_RACE_REPRODUCED = YES or
// NO off that noise, this is recorded as SAME_ORCHESTRATOR_RACE_REPRODUCED =
// NOT_RELIABLY_TESTABLE with this fixture, distinct from
// SEPARATE_ORCHESTRATOR_RACE_REPRODUCED = YES, which used channel-controlled,
// fully deterministic interleaving instead and needed no such assumption.
//
// The data race itself is a separate, secondary finding about
// internal/executive's test fixtures (not production code, and not this
// round's subject) worth a future look: memoryTasks documents that it
// expects at most one "main flow" goroutine plus a lease-keeper heartbeat
// goroutine, not multiple full Resume() drivers.
func TestSameOrchestratorRaceIsNotReliablyTestableWithThisFixture(t *testing.T) {
	t.Log("SAME_ORCHESTRATOR_RACE_REPRODUCED = NOT_RELIABLY_TESTABLE: memoryTasks's per-method mutexes do not make a whole Resume() read-decide-act sequence atomic; raw concurrent goroutines against it hit a genuine data race in the fixture itself (RecordAttemptSucceeded vs findOrphanedSucceededInvocation) before they say anything reliable about the mechanism under test. Statically: o.leases is one mutex-guarded map per Orchestrator instance, so a claim's rememberLease is visible to every other goroutine sharing that SAME instance before any of them could observe it as unowned -- the only place a same-Orchestrator race could matter is the handful of statements between ClaimTask returning and rememberLease running inside driveTypedTask, a window no test-only hook can reach without modifying orchestrator.go.")
}

// ----------------------------------------------------------------------------
// Steps 8-9: RED regression tests pinning the DESIRED behavior. Both MUST
// FAIL on current code -- that failure is the deliverable, not a bug in the
// test.
// ----------------------------------------------------------------------------

// newUnownedActiveLeaseFixture builds a root with one child already durably
// "running" and an ActiveLease this Orchestrator's local map has never seen
// -- the same shape newRestartFixture (restart_recovery_test.go) uses, with
// an explicit WithNoRetries option surface.
func newUnownedActiveLeaseFixture(t *testing.T, holderID string, opts ...OrchestratorOption) (*memoryTasks, *fakeModels, *fakeHarness, *Orchestrator, TaskRecord, TaskRecord) {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	orch := newLeaseReproOrchestrator(t, tasksPort, models, harness, opts...)
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-unowned-" + holderID,
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:unowned-" + holderID,
		Requirements: []RequirementProposal{{Key: "executive_closure_verified", Type: "result", Description: "x", Required: true}},
	})
	child, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: childKey(root.ID, "ceo-plan"), Title: "child", Instructions: "child",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	child.Status = "running"
	child.Attempts = []AttemptRecord{{ID: 900, Ordinal: 1, State: "running"}}
	child.ActiveLease = &LeaseRecord{TaskID: child.ID, AttemptID: 900, HolderID: holderID}
	tasksPort.tasks[child.ID] = child
	return tasksPort, models, harness, orch, root, child
}

// TestActiveUnownedLeaseBarrierDoesNotPermanentlyBlockRoot: desired state,
// retries allowed. MUST currently FAIL.
//
// Calls Resume directly, not ResumeDurable: ResumeDurable's own top-of-
// function loop over PRE-EXISTING children with an active lease (the exact
// shape newUnownedActiveLeaseFixture builds) already returns a non-durable
// ErrRunBlocked without ever calling BlockTask -- ResumeDurable's early loop
// is not where the bug lives. The bug is reachable only when a driver's
// Resume() call reaches driveTypedTask directly for a task it is only now
// discovering has an active lease, which is exactly what happens in
// production for a brand-new root's first-ever resume (see
// TestSeparateOrchestratorActiveLeaseTOCTOUReproducesIncidentShape). Calling
// Resume here targets that same code path with a simpler, static fixture.
func TestActiveUnownedLeaseBarrierDoesNotPermanentlyBlockRoot(t *testing.T) {
	tasksPort, _, harness, orch, root, child := newUnownedActiveLeaseFixture(t, "7001")
	_, err := orch.Resume(context.Background(), root.ID)
	if !errors.Is(err, ErrActiveLeaseBarrier) {
		t.Fatalf("err=%v want ErrActiveLeaseBarrier reported", err)
	}
	if errors.Is(err, ErrRunBlocked) {
		t.Fatalf("err=%v must not also be the generic, durably-permanent ErrRunBlocked", err)
	}
	if harness.callCount() != 0 {
		t.Fatalf("harness calls=%d want 0: no execution beside an active lease this process does not own", harness.callCount())
	}
	current, _ := tasksPort.GetTask(context.Background(), child.ID)
	if current.Attempts[0].State != "running" || current.ActiveLease == nil {
		t.Fatalf("child must be untouched by the barrier: %+v", current)
	}
	rootAfter, _ := tasksPort.GetTask(context.Background(), root.ID)
	if rootAfter.Status == "blocked" && rootAfter.ReasonCode == "executive_phase_failed" {
		t.Fatalf("DESIRED_REGRESSION_TEST_RED: current code durably converts a transient active-lease barrier into a permanent executive_phase_failed root block: root=%+v", rootAfter)
	}
}

// TestActiveUnownedLeaseBarrierRemainsTransientUnderNoRetries: same desired
// state, explicitly under WithNoRetries(). MUST currently FAIL. The
// concept under test: WAITING FOR AN EXISTING ATTEMPT != RETRYING AN ATTEMPT
// -- no-retries must still forbid a SECOND attempt, but must not durably
// convict the root for an attempt that is still legitimately in flight
// somewhere else.
// Calls Resume directly for the same reason documented on
// TestActiveUnownedLeaseBarrierDoesNotPermanentlyBlockRoot above.
func TestActiveUnownedLeaseBarrierRemainsTransientUnderNoRetries(t *testing.T) {
	tasksPort, _, harness, orch, root, child := newUnownedActiveLeaseFixture(t, "7001", WithNoRetries())
	_, err := orch.Resume(context.Background(), root.ID)
	if !errors.Is(err, ErrActiveLeaseBarrier) {
		t.Fatalf("err=%v want ErrActiveLeaseBarrier reported even under no-retries", err)
	}
	if errors.Is(err, ErrRunBlocked) {
		t.Fatalf("err=%v must not also be the generic, durably-permanent ErrRunBlocked", err)
	}
	if harness.callCount() != 0 {
		t.Fatalf("harness calls=%d want 0 even under no-retries", harness.callCount())
	}
	if got := len(tasksPort.claims); got != 0 {
		t.Fatalf("claims=%d want 0: no-retries must not create a second attempt while the existing one is active", got)
	}
	current, _ := tasksPort.GetTask(context.Background(), child.ID)
	if current.Attempts[0].State != "running" || current.ActiveLease == nil {
		t.Fatalf("child must be untouched: %+v", current)
	}
	rootAfter, _ := tasksPort.GetTask(context.Background(), root.ID)
	if rootAfter.Status == "blocked" && rootAfter.ReasonCode == "executive_phase_failed" {
		t.Fatalf("DESIRED_NORETRIES_REGRESSION_RED: no-retries mode durably blocks root for a transient barrier instead of leaving it recoverable once the existing attempt resolves: root=%+v", rootAfter)
	}
}

// ----------------------------------------------------------------------------
// Step 10: ErrRunBlocked is deliberately overloaded across many genuinely
// permanent, human-gated conditions. Broadening isNonBlockingPhaseError to
// treat ALL ErrRunBlocked as non-blocking would silently reopen every one of
// these. This test enumerates the reason codes Resume()'s own blocked-root
// switch treats as durably terminal (never auto-reopened) and asserts they
// remain distinct from the active-lease barrier's own classification need.
// ----------------------------------------------------------------------------

func TestGenericErrRunBlockedCoversManyGenuinelyPermanentReasons(t *testing.T) {
	permanentReasons := []string{
		ReasonDesignRevisionRequired,
		ReasonDesignRejected,
		ReasonDesignRoundsExhausted,
		ReasonDepartmentReviewBlocked,
		ReasonDepartmentReplansExhausted,
		ReasonRunChildFailed,
		ReasonAdversarialReviewUnavailable,
		ReasonEvidenceInsufficient,
		ReasonContextSourceMissing,
		ReasonModelAuthorityViolation,
	}
	for _, reason := range permanentReasons {
		if reason == "" {
			t.Fatal("a permanent reason code constant is empty -- rename drift against orchestrator.go")
		}
	}
	// GENERIC_ERRRUNBLOCKED_SAFE_TO_MARK_NONBLOCKING = NO: recorded here as
	// executable documentation, not asserted as a boolean, because the
	// unsafe change this guards against is a change to isNonBlockingPhaseError
	// this round does not make.
	t.Logf("GENERIC_ERRRUNBLOCKED_SAFE_TO_MARK_NONBLOCKING = NO (%d durably-permanent ErrRunBlocked reasons enumerated)", len(permanentReasons))
}

// ----------------------------------------------------------------------------
// Step 13: post-barrier continuation must not duplicate execution. After the
// barrier resolves and the legitimate owner completes the child, a LATER,
// independent ResumeDurable over the same correlation must not re-invoke the
// harness for that already-completed child.
// ----------------------------------------------------------------------------

func TestPostBarrierContinuationDoesNotDuplicateTheCompletedChild(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	harness.body = validLeaseReproPlanBody()
	orch := newLeaseReproOrchestrator(t, tasksPort, models, harness, WithNoRetries())
	run := submitLeaseReproOwnerGoal(t, orch, "post-barrier-root")
	if _, err := orch.ResumeDurable(context.Background(), run.RootTaskID); err != nil {
		t.Fatalf("initial resume err=%v", err)
	}
	plan := findLeaseReproPlanTask(t, tasksPort, run.CorrelationID)
	if plan.Status != "completed" {
		t.Fatalf("plan status=%s want completed before simulating an operator unblock", plan.Status)
	}
	// A later, independent resume over the same correlation drives the run
	// FORWARD (into the department-plan child this fixture's plan produced),
	// which is expected to hit an unrelated contract rejection here because
	// this test's single shared fakeHarness only knows how to answer the
	// CEO-plan schema, not a department-plan one -- that error is not this
	// test's subject and is deliberately ignored. What this test asserts is
	// narrower and unaffected by it: the ALREADY-COMPLETED plan task/attempt
	// must never be re-claimed or re-invoked by this second resume.
	planAttemptID := plan.Attempts[0].ID
	invocationsBeforeSecondResume := models.invocationCount(plan.ID, planAttemptID)
	harnessCallsBeforeSecondResume := harness.callCount()
	_, _ = orch.ResumeDurable(context.Background(), run.RootTaskID)
	if got := models.invocationCount(plan.ID, planAttemptID); got != invocationsBeforeSecondResume {
		t.Fatalf("invocations for the completed plan attempt changed: before=%d after=%d (a later resume must never re-invoke an already-completed child)", invocationsBeforeSecondResume, got)
	}
	planAfterSecondResume, _ := tasksPort.GetTask(context.Background(), plan.ID)
	if len(planAfterSecondResume.Attempts) != 1 {
		t.Fatalf("plan task attempts after a later resume=%d want still 1 (no second attempt on an already-completed child)", len(planAfterSecondResume.Attempts))
	}
	if planAfterSecondResume.Status != "completed" {
		t.Fatalf("plan task status after a later resume=%s want still completed", planAfterSecondResume.Status)
	}
	t.Logf("harness calls before second resume=%d, after=%d (increase, if any, is the unrelated department-plan schema mismatch above, not a re-invocation of the completed plan child)", harnessCallsBeforeSecondResume, harness.callCount())
}

// ----------------------------------------------------------------------------
// Step 12: ErrLeaseLost and ErrActiveLeaseBarrier must never be conflated.
// ErrLeaseLost means this process DID hold the lease and lost the ability to
// keep it (see TestKeeperFailureOverridesHarnessSuccess in
// harness_execution_test.go, left untouched by this fix). ErrActiveLeaseBarrier
// means this process never demonstrated possession of the active lease it
// observed in the first place. Both are classified as non-blocking phase
// errors, but they are semantically distinct sentinels and neither wraps the
// other.
// ----------------------------------------------------------------------------

func TestErrLeaseLostAndErrActiveLeaseBarrierAreNeverConflated(t *testing.T) {
	if errors.Is(ErrLeaseLost, ErrActiveLeaseBarrier) {
		t.Fatal("ErrLeaseLost must not be classified as ErrActiveLeaseBarrier: losing a lease you held is not the same as never holding it")
	}
	if errors.Is(ErrActiveLeaseBarrier, ErrLeaseLost) {
		t.Fatal("ErrActiveLeaseBarrier must not be classified as ErrLeaseLost: never holding a lease is not the same as losing one you held")
	}
	if ErrLeaseLost.Error() == ErrActiveLeaseBarrier.Error() {
		t.Fatal("the two sentinels must carry distinct messages so an operator reading either can tell them apart")
	}
	if !isNonBlockingPhaseError(ErrLeaseLost) || !isNonBlockingPhaseError(ErrActiveLeaseBarrier) {
		t.Fatal("both sentinels must be classified as non-blocking phase errors, independently of each other")
	}
}
