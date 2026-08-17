package executive

import (
	"context"
	"errors"
	"testing"
)

// readyAfterCrashFixture is the shape the two reconcilers can produce between
// them: the Task Engine has already expired the lease and made the task
// retryable, while Model Runtime has not yet classified the invocation the
// dead attempt left behind.
func readyAfterCrashFixture(t *testing.T, priorStatus string, mayHaveStarted bool) *harnessFixture {
	t.Helper()
	fixture := newHarnessFixture(t)
	task, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
	task.Status = "ready"
	task.AttemptCount = 1
	task.Attempts = []AttemptRecord{{ID: 900, Ordinal: 1, State: "lease_expired"}}
	fixture.tasks.tasks[task.ID] = task
	fixture.task = task
	fixture.models.setInvocations(task.ID, 900, InvocationRecord{
		ID: 771, TaskID: task.ID, AttemptID: 900, Status: priorStatus,
		ProviderExecutionMayHaveStarted: mayHaveStarted,
	})
	return fixture
}

// TestUnresolvedProviderExecutionBlocksAFreshAttempt is the case-B guard.
//
// The lease expired and the task is ready again, so nothing in task state
// stands in the way any more. What stands in the way is that the previous
// attempt's request may already have crossed the provider boundary and nobody
// has classified it yet. A second call here would be a duplicate execution
// whose external effect nobody could account for.
func TestUnresolvedProviderExecutionBlocksAFreshAttempt(t *testing.T) {
	for _, status := range []string{"send_started", "response_received"} {
		t.Run(status, func(t *testing.T) {
			fixture := readyAfterCrashFixture(t, status, true)
			_, err := fixture.drive(t)
			if !errors.Is(err, ErrPriorExecutionUnresolved) {
				t.Fatalf("err=%v want the fresh attempt to fail closed", err)
			}
			if fixture.harness.callCount() != 0 {
				t.Fatalf("harness runs=%d: a second provider execution was started", fixture.harness.callCount())
			}
			if len(fixture.tasks.claims) != 0 {
				t.Fatalf("claims=%d: a fresh attempt was created beside an unresolved execution", len(fixture.tasks.claims))
			}
			if fixture.budget.count() != 0 {
				t.Fatalf("budget consulted %d times for work that never began", fixture.budget.count())
			}
			// Nothing durable moved, and the failure stays retryable so the run
			// resumes once Model Runtime has classified the old call.
			current, _ := fixture.tasks.GetTask(context.Background(), fixture.task.ID)
			if current.Status != "ready" || len(current.Attempts) != 1 {
				t.Fatalf("task=%+v: the barrier must not mutate durable state", current)
			}
			if !isNonBlockingPhaseError(ErrPriorExecutionUnresolved) {
				t.Fatal("an unresolved prior execution must stay retryable, not block the root")
			}
		})
	}
}

// TestModelRuntimeReconciliationTurnsTheBarrierIntoAmbiguous walks the full
// hand-off: the Executive waits, Model Runtime classifies the old call, and the
// run then blocks on that verdict instead of retrying it. The provider call
// count never moves.
func TestModelRuntimeReconciliationTurnsTheBarrierIntoAmbiguous(t *testing.T) {
	fixture := readyAfterCrashFixture(t, "send_started", true)
	if _, err := fixture.drive(t); !errors.Is(err, ErrPriorExecutionUnresolved) {
		t.Fatalf("err=%v", err)
	}
	// Model Runtime reconciles the expired send: ambiguous, not retryable.
	// The Executive does not perform this transition and does not model it --
	// it only reads the result.
	fixture.models.setInvocations(fixture.task.ID, 900, InvocationRecord{
		ID: 771, TaskID: fixture.task.ID, AttemptID: 900, Status: "ambiguous",
	})
	_, err := fixture.drive(t)
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("err=%v want model_outcome_ambiguous", err)
	}
	root, _ := fixture.tasks.GetTask(context.Background(), fixture.root.ID)
	if root.Status != "blocked" || root.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("root=%+v", root)
	}
	if fixture.harness.callCount() != 0 {
		t.Fatalf("harness runs=%d across the whole hand-off", fixture.harness.callCount())
	}
	if len(fixture.tasks.claims) != 0 {
		t.Fatalf("claims=%d: no attempt may be created for an ambiguous outcome", len(fixture.tasks.claims))
	}
}

// TestResolvedPriorExecutionDoesNotBlockARetry is the control: the barrier must
// only stop work that is genuinely unresolved. A previous attempt that failed
// before the provider was reached is exactly what retries exist for.
func TestResolvedPriorExecutionDoesNotBlockARetry(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "requested", "claimed"} {
		t.Run(status, func(t *testing.T) {
			fixture := readyAfterCrashFixture(t, status, false)
			if _, err := fixture.drive(t); err != nil {
				t.Fatalf("a resolved prior execution must not block a retry: %v", err)
			}
			if fixture.harness.callCount() != 1 {
				t.Fatalf("harness runs=%d want 1", fixture.harness.callCount())
			}
			if len(fixture.tasks.claims) != 1 {
				t.Fatalf("claims=%d want a fresh attempt", len(fixture.tasks.claims))
			}
		})
	}
}
