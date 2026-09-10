package executive

import (
	"context"
	"errors"
	"testing"
)

// newRetryScheduledFixture builds a root task (id 99) and one worker child
// task (id 1, sharing the root's correlation) so handlePhaseError's own
// root-blocking decision can be observed directly, not just the error value
// failAttempt/handleHarnessFailure produce. attemptCount/maxAttempts control
// which of retry_wait/failed newMemoryTasks.RecordAttemptFailed lands the
// child on for a retryable failure.
func newRetryScheduledFixture(t *testing.T, retryable bool, attemptCount, maxAttempts int) (*Orchestrator, *memoryTasks, TaskRecord, TaskRecord) {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	const invocationID = int64(900)
	models.retryableFailures[invocationID] = retryable
	const rootID int64 = 99
	const childID int64 = 1
	const correlation = "executive:campaign-retry-scheduled"
	root := TaskRecord{ID: rootID, Status: "running", CorrelationID: correlation, AssignedRoleID: CEORoleID}
	child := TaskRecord{
		ID: childID, Status: "running", CorrelationID: correlation, AttemptCount: attemptCount, MaxAttempts: maxAttempts,
		ActiveLease: &LeaseRecord{TaskID: childID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"},
	}
	tasksPort.tasks[rootID] = root
	tasksPort.tasks[childID] = child
	return &Orchestrator{tasks: tasksPort, models: models}, tasksPort, root, child
}

// TestRetryableFailureWithAttemptsRemainingSchedulesRetryWithoutBlockingRoot
// covers B4's first required case: RecordAttemptFailed leaves the task in
// retry_wait, ErrTaskRetryScheduled is present alongside the original
// ErrCompletionFailed sentinel (errors.Is holds for both), and
// handlePhaseError does not block the root -- the exact gap this round's
// E2E test found (a retryable provider failure blocking the whole campaign
// instead of letting the task engine's own already-correct retry carry it).
func TestRetryableFailureWithAttemptsRemainingSchedulesRetryWithoutBlockingRoot(t *testing.T) {
	orchestrator, tasksPort, root, child := newRetryScheduledFixture(t, true, 1, 3)
	_, err := orchestrator.handleHarnessFailure(context.Background(), root, child,
		LeaseRecord{TaskID: child.ID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}, "actor",
		HarnessRunOutcome{Status: HarnessRunFailed, Failure: HarnessFailureModelError, InvocationID: 900, TerminationReason: "provider at capacity"})
	if err == nil {
		t.Fatal("a model failure must still surface as an error to the caller")
	}
	if !errors.Is(err, ErrTaskRetryScheduled) {
		t.Fatalf("expected ErrTaskRetryScheduled, got %v", err)
	}
	if !errors.Is(err, ErrCompletionFailed) {
		t.Fatalf("expected the original ErrCompletionFailed sentinel preserved, got %v", err)
	}
	if !isNonBlockingPhaseError(err) {
		t.Fatalf("isNonBlockingPhaseError must recognize ErrTaskRetryScheduled: %v", err)
	}
	if got := tasksPort.statusOf(child.ID); got != "retry_wait" {
		t.Fatalf("child task is %q, want retry_wait", got)
	}

	if _, herr := orchestrator.handlePhaseError(context.Background(), root, child, err); !errors.Is(herr, err) {
		t.Fatalf("handlePhaseError must return the same non-blocking error, got %v", herr)
	}
	if got := tasksPort.statusOf(root.ID); got == "blocked" {
		t.Fatal("handlePhaseError blocked the root for a retry the task engine already scheduled")
	}
}

// TestNonRetryableFailureStillBlocksTheRoot covers B4's second required
// case: a non-retryable provider failure must keep today's blocking
// behavior unchanged -- ErrTaskRetryScheduled must never appear for it.
func TestNonRetryableFailureStillBlocksTheRoot(t *testing.T) {
	orchestrator, tasksPort, root, child := newRetryScheduledFixture(t, false, 1, 3)
	_, err := orchestrator.handleHarnessFailure(context.Background(), root, child,
		LeaseRecord{TaskID: child.ID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}, "actor",
		HarnessRunOutcome{Status: HarnessRunFailed, Failure: HarnessFailureModelError, InvocationID: 900, TerminationReason: "provider rejected the request"})
	if err == nil {
		t.Fatal("a model failure must still surface as an error to the caller")
	}
	if errors.Is(err, ErrTaskRetryScheduled) {
		t.Fatalf("a non-retryable failure must never carry ErrTaskRetryScheduled: %v", err)
	}
	if isNonBlockingPhaseError(err) {
		t.Fatalf("a non-retryable failure must still be a blocking phase error: %v", err)
	}
	if got := tasksPort.statusOf(child.ID); got != "failed" {
		t.Fatalf("child task is %q, want failed", got)
	}

	if _, herr := orchestrator.handlePhaseError(context.Background(), root, child, err); herr == nil {
		t.Fatal("expected handlePhaseError to surface the blocking outcome")
	}
	if got := tasksPort.statusOf(root.ID); got != "blocked" {
		t.Fatalf("root task is %q, want blocked -- a non-retryable worker failure must still reach department review through the normal blocking path", got)
	}
}

// TestRetryableFailureOnTheLastAttemptDoesNotClaimRetryScheduled covers
// B4's third required case: a retryable provider failure is not itself
// proof of a scheduled retry. When it exhausts max_attempts the task engine
// leaves the task in a terminal state (this fake's stand-in for
// dead_letter is "failed", mirrored from RecordAttemptFailed's own
// AttemptCount>=MaxAttempts branch) -- and that must NOT carry
// ErrTaskRetryScheduled, or a worker whose retry budget is genuinely spent
// would leave the root silently unblocked with no department review ever
// seeing the terminal failure.
func TestRetryableFailureOnTheLastAttemptDoesNotClaimRetryScheduled(t *testing.T) {
	orchestrator, tasksPort, root, child := newRetryScheduledFixture(t, true, 3, 3)
	_, err := orchestrator.handleHarnessFailure(context.Background(), root, child,
		LeaseRecord{TaskID: child.ID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}, "actor",
		HarnessRunOutcome{Status: HarnessRunFailed, Failure: HarnessFailureModelError, InvocationID: 900, TerminationReason: "provider at capacity, budget exhausted"})
	if err == nil {
		t.Fatal("a model failure must still surface as an error to the caller")
	}
	if errors.Is(err, ErrTaskRetryScheduled) {
		t.Fatalf("a retryable failure with no attempts left must not claim a retry was scheduled: %v", err)
	}
	if isNonBlockingPhaseError(err) {
		t.Fatalf("an exhausted retry budget must still be a blocking phase error: %v", err)
	}
	if got := tasksPort.statusOf(child.ID); got == "retry_wait" {
		t.Fatal("fixture setup error: child task must be terminal (attempts exhausted), not retry_wait")
	}

	if _, herr := orchestrator.handlePhaseError(context.Background(), root, child, err); herr == nil {
		t.Fatal("expected handlePhaseError to surface the blocking outcome")
	}
	if got := tasksPort.statusOf(root.ID); got != "blocked" {
		t.Fatalf("root task is %q, want blocked -- an exhausted retry budget must still reach department review", got)
	}
}

// TestWithNoRetriesSingleAttemptDoesNotScheduleRetry covers B4's fourth
// required case: a one-shot task (max_attempts=1) that fails retryably has
// no attempt left the instant it fails -- RecordAttemptFailed's own
// AttemptCount(1)>=MaxAttempts(1) branch lands it on the terminal status
// immediately, never retry_wait, so ErrTaskRetryScheduled must never
// appear regardless of how "retryable" the provider outcome looked.
func TestWithNoRetriesSingleAttemptDoesNotScheduleRetry(t *testing.T) {
	orchestrator, tasksPort, root, child := newRetryScheduledFixture(t, true, 1, 1)
	_, err := orchestrator.handleHarnessFailure(context.Background(), root, child,
		LeaseRecord{TaskID: child.ID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}, "actor",
		HarnessRunOutcome{Status: HarnessRunFailed, Failure: HarnessFailureModelError, InvocationID: 900, TerminationReason: "provider at capacity"})
	if err == nil {
		t.Fatal("a model failure must still surface as an error to the caller")
	}
	if errors.Is(err, ErrTaskRetryScheduled) {
		t.Fatalf("a max_attempts=1 task must never claim a retry was scheduled: %v", err)
	}
	if got := tasksPort.statusOf(child.ID); got == "retry_wait" {
		t.Fatal("fixture setup error: a max_attempts=1 task must not reach retry_wait")
	}
}
