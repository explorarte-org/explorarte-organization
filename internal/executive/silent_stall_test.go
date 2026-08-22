package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-013 stopped and said nothing.
//
// Its round-2 department review exhausted its five attempts and dead-lettered
// -- five different plans, each refused for the same undocumented rule.
// driveTypedTask has branches for completed, awaiting_verification, ready,
// leased and running; a dead-lettered task matched none of them and fell out
// of the bottom at "return task, nil" -- no progress and, crucially, no error.
// The worker logs through a failure observer that only fires on an error, so a
// pass that did nothing successfully was indistinguishable from no pass at
// all. The root stayed ready for eleven minutes with nothing recorded.
//
// The projector had reported the run as failed the whole time. Nothing read
// that projection.
//
// The rule that fixes it has to be narrow. A first version stopped the run on
// ANY terminal child, which made failures contagious again -- the exact defect
// 6eaa74e removed -- and would have let a dead-lettered engineering mission
// block its campaign forever, outliving the successor recovery opened for it.
func newTypedTaskFixture(t *testing.T, status string) (*Orchestrator, *memoryTasks, TaskRecord, TaskRecord) {
	t.Helper()
	tasksPort := newMemoryTasks()
	const rootID int64 = 1
	const childID int64 = 2
	root := TaskRecord{
		ID: rootID, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: CEORoleID, TaskClass: TaskClassOwnerGoal,
		CorrelationID: "executive:stalled", Status: "ready", MaxAttempts: 2,
	}
	child := TaskRecord{
		ID: childID, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: "ingenieria_ia/orquestador",
		CorrelationID:  "executive:stalled", Status: status,
		ReasonCode: "model_result_contract_rejected",
	}
	tasksPort.tasks[rootID] = root
	tasksPort.tasks[childID] = child
	return &Orchestrator{tasks: tasksPort, registry: fakeRegistry{rev: RevisionRef{ID: 7}}}, tasksPort, root, child
}

// PROPERTY 3: the review that killed AUTONOMY-SMOKE-013 leaves a durable
// reason instead of a silently ready root.
func TestAHostDrivenStepThatDiesStopsTheRunOutLoud(t *testing.T) {
	orchestrator, tasksPort, root, child := newTypedTaskFixture(t, "dead_letter")
	_, err := orchestrator.driveTypedTask(context.Background(), root, child,
		departmentReviewOutputSchema, PurposeDepartmentReview, nil)
	if !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a dead host-driven step must surface as an error, got %v", err)
	}
	stopped := tasksPort.tasks[root.ID]
	if stopped.Status != "blocked" {
		t.Fatalf("root is %q: it must not sit ready with nothing recorded", stopped.Status)
	}
	if stopped.ReasonCode != ReasonRunChildFailed {
		t.Fatalf("reason_code=%q, want %q", stopped.ReasonCode, ReasonRunChildFailed)
	}
	// "Something failed" is the same silence in a different shape.
	for _, needed := range []string{"department-review", "2", "model_result_contract_rejected"} {
		if !strings.Contains(stopped.Reason, needed) {
			t.Fatalf("the reason must name %q, got %q", needed, stopped.Reason)
		}
	}
}

// The stop is dead_letter alone, and that boundary is the point.
//
// "failed" looks terminal and is not the end of the story: a model that
// answers perfectly while its output fails the typed contract is recorded
// failed-but-retryable precisely so the task can try again, and an authority
// outage schedules its own re-entry. An earlier version of this rule stopped
// on every terminal status and broke exactly that, which is how this test
// exists.
func TestAHostDrivenStepThatCanStillComeBackDoesNotStopTheRun(t *testing.T) {
	for _, status := range []string{"failed", "retry_wait", "blocked"} {
		t.Run(status, func(t *testing.T) {
			orchestrator, tasksPort, root, child := newTypedTaskFixture(t, status)
			if _, err := orchestrator.driveTypedTask(context.Background(), root, child,
				departmentReviewOutputSchema, PurposeDepartmentReview, nil); errors.Is(err, ErrRunBlocked) {
				t.Fatalf("%q still has governed re-entry and must not end the run", status)
			}
			if got := tasksPort.tasks[root.ID].ReasonCode; got == ReasonRunChildFailed {
				t.Fatalf("%q must not stop the run", status)
			}
		})
	}
}

// PROPERTY 1: a failed worker still reaches its department's review.
//
// This is 6eaa74e's invariant: a failure belongs to the worker that had it.
// The review is equipped to judge it -- its summary carries each worker's
// status -- and its verdict already has needs_replan, blocked and fail. A run
// stopped here would replace that judgement with a stall.
func TestAFailedWorkerDoesNotStopTheRun(t *testing.T) {
	for _, status := range []string{"dead_letter", "failed"} {
		t.Run(status, func(t *testing.T) {
			orchestrator, tasksPort, root, worker := newTypedTaskFixture(t, status)
			returned, err := orchestrator.driveTypedTask(context.Background(), root, worker,
				workerResultOutputSchema, PurposeDepartmentWorker, nil)
			if err != nil {
				t.Fatalf("a failed worker must not surface as a run failure: %v", err)
			}
			if returned.Status != status {
				t.Fatalf("the worker's own outcome must be left alone, got %q", returned.Status)
			}
			if got := tasksPort.tasks[root.ID].Status; got != "ready" {
				t.Fatalf("root is %q: a worker's failure belongs to the worker, not its parent", got)
			}
			// It stays terminal, which is what lets the phase close and the
			// review run at all.
			if !allDepartmentWorkersTerminal([]TaskRecord{worker}, root.ID, "") {
				t.Fatal("a terminal worker must let its phase close so the review can judge it")
			}
		})
	}
}

// PROPERTY 2: a dead-lettered engineering mission does not block its campaign,
// so the successor recovery opens for it can run.
//
// Missions are provisioned, not driven through driveTypedTask, and since they
// now carry their campaign's correlation they stay in the run's children
// forever. A rule that stopped a run on any terminal child would therefore
// have made a recovered mission's campaign permanently blocked -- closing the
// silent stall by breaking recovery.
func TestADeadLetteredMissionDoesNotBlockItsCampaign(t *testing.T) {
	tasksPort := newMemoryTasks()
	const rootID, missionID, successorID int64 = 1, 2, 3
	tasksPort.tasks[rootID] = TaskRecord{
		ID: rootID, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: CEORoleID, TaskClass: TaskClassOwnerGoal,
		CorrelationID: "executive:recovered", Status: "ready", MaxAttempts: 2,
	}
	// The failure that recovery answered: terminal forever, by design.
	tasksPort.tasks[missionID] = TaskRecord{
		ID: missionID, TaskClass: TaskClassGeneralWork, AssignedRoleID: "ingenieria_ia/code-runner",
		CorrelationID: "executive:recovered", Status: "dead_letter", ReasonCode: "max_attempts_exhausted",
	}
	// The successor episode, alive and carrying the same campaign.
	tasksPort.tasks[successorID] = TaskRecord{
		ID: successorID, TaskClass: TaskClassGeneralWork, AssignedRoleID: "ingenieria_ia/code-runner",
		CorrelationID: "executive:recovered", Status: "ready", CausationID: "task:2",
	}
	orchestrator := &Orchestrator{tasks: tasksPort, registry: fakeRegistry{rev: RevisionRef{ID: 7}}}

	// Resume is allowed to fail here for reasons of its own -- this fixture
	// carries no plan, no limits and no harness, so CEO planning cannot get
	// far. What is asserted is the only thing this test is about: whatever
	// else happens, the dead mission did not end the campaign.
	_, _ = orchestrator.Resume(context.Background(), rootID)
	stopped := tasksPort.tasks[rootID]
	if stopped.ReasonCode == ReasonRunChildFailed {
		t.Fatalf("a dead-lettered mission must not stop the campaign recovery is already recovering: %q", stopped.Reason)
	}

	// And the structural reason it cannot: a mission is provisioned, never
	// driven through the typed path, so the rule that stops a run never sees
	// it. Driving one as a worker -- the closest thing to how it is executed
	// -- is likewise not a run failure.
	mission := tasksPort.tasks[missionID]
	if _, err := orchestrator.driveTypedTask(context.Background(), tasksPort.tasks[rootID], mission,
		workerResultOutputSchema, PurposeDepartmentWorker, nil); err != nil {
		t.Fatalf("a terminal mission must not surface as a run failure: %v", err)
	}
}

// PROPERTY 4: a stop stays stopped. Reopening would walk straight back into
// the pass that found the dead step and block again, one model call poorer
// each time it re-drove whatever came before it.
func TestAStoppedRunIsNotSilentlyReopened(t *testing.T) {
	orchestrator, tasksPort, root, child := newTypedTaskFixture(t, "dead_letter")
	if _, err := orchestrator.driveTypedTask(context.Background(), root, child,
		departmentReviewOutputSchema, PurposeDepartmentReview, nil); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("setup: %v", err)
	}
	if _, err := orchestrator.Resume(context.Background(), root.ID); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a stopped run must stay stopped, got %v", err)
	}
	if got := tasksPort.tasks[root.ID].Status; got != "blocked" {
		t.Fatalf("root is %q after a second pass, want blocked", got)
	}
}

// A child that is merely waiting is not a child that failed. Blocking on one
// would turn every request for a human into the end of the campaign.
func TestWaitingIsNotFailing(t *testing.T) {
	for _, status := range []string{"blocked", "retry_wait", "ready", "running", "completed", "no_action"} {
		if isFailedTask(status) {
			t.Fatalf("%q must not count as a terminal failure", status)
		}
	}
}
