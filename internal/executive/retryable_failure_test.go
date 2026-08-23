package executive

import (
	"context"
	"testing"
)

// A task carrying max_attempts=3 that dies on attempt 1 of a TRANSIENT
// provider failure is a task whose retry budget was never real.
//
// AUTONOMY-SMOKE-001's adversarial review is the case: xAI reported
// resource-exhausted, Model Runtime recorded the outcome as retryable, and the
// task went straight to failed having spent one of its three attempts. The
// flag was written and nobody read it.
//
// The assertion is the task's own status, not the flag that produced it: a
// retryable failure with attempts remaining parks the task for another try,
// and that is the behaviour a campaign survives on.
func TestATransientProviderFailureSpendsAnAttemptNotTheTask(t *testing.T) {
	for _, tc := range []struct {
		name       string
		retryable  bool
		wantStatus string
	}{
		{"provider was momentarily at capacity", true, "retry_wait"},
		{"provider rejected the request itself", false, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orchestrator, tasksPort, taskID := newFailureFixture(t, 900, tc.retryable)

			_, err := orchestrator.handleHarnessFailure(context.Background(), TaskRecord{ID: 99},
				TaskRecord{ID: taskID}, LeaseRecord{TaskID: taskID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}, "actor",
				HarnessRunOutcome{Status: HarnessRunFailed, Failure: HarnessFailureModelError,
					InvocationID: 900, TerminationReason: "provider at capacity"})
			if err == nil {
				t.Fatal("a model failure must still surface as an error to the caller")
			}
			if got := tasksPort.statusOf(taskID); got != tc.wantStatus {
				t.Fatalf("task is %q, want %q: with 2 of 3 attempts left, whether the campaign survives depends entirely on this", got, tc.wantStatus)
			}
		})
	}
}

// With no outcome to consult, the Executive must not invent one. Spending an
// attempt on a guess can repeat a call the provider may already have billed.
func TestAnUnreadableOutcomeIsNotRetried(t *testing.T) {
	orchestrator, tasksPort, taskID := newFailureFixture(t, 0, false)

	_, err := orchestrator.handleHarnessFailure(context.Background(), TaskRecord{ID: 99},
		TaskRecord{ID: taskID}, LeaseRecord{TaskID: taskID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}, "actor",
		// No invocation was recorded, so there is no outcome to read.
		HarnessRunOutcome{Status: HarnessRunFailed, Failure: HarnessFailureModelError, TerminationReason: "no invocation"})
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	if got := tasksPort.statusOf(taskID); got != "failed" {
		t.Fatalf("task is %q: with nothing to read, a failure must not be treated as transient", got)
	}
}

func newFailureFixture(t *testing.T, invocationID int64, retryable bool) (*Orchestrator, *memoryTasks, int64) {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	if invocationID > 0 {
		models.retryableFailures[invocationID] = retryable
	}
	const taskID int64 = 1
	tasksPort.tasks[taskID] = TaskRecord{ID: taskID, Status: "running", AttemptCount: 1, MaxAttempts: 3,
		ActiveLease: &LeaseRecord{TaskID: taskID, AttemptID: 2, LeaseToken: "lease-2", HolderID: "actor"}}
	return &Orchestrator{tasks: tasksPort, models: models}, tasksPort, taskID
}

func (m *memoryTasks) statusOf(id int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id].Status
}
