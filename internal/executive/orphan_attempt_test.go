package executive

import (
	"context"
	"testing"
)

// Orphaned means nobody recorded what happened: a result exists and no process
// ever claimed it. An attempt the orchestrator itself terminated is the
// opposite of that, whatever its invocation produced.
//
// The distinction is not academic. A model can answer perfectly while its
// OUTPUT fails the typed contract; that is recorded as
// model_result_contract_rejected and left retryable so the task tries again,
// which deliberately leaves a succeeded invocation with no adoptable lease.
// AUTONOMY-SMOKE-001's root 213 was declared unrecoverable for exactly that,
// while its department review was on its way to a second attempt.
func TestAnAdjudicatedAttemptIsNotAnOrphan(t *testing.T) {
	for _, state := range []string{"failed", "cancelled", "finished"} {
		t.Run(state, func(t *testing.T) {
			orchestrator, _ := newOrphanFixture(state)
			_, found, err := orchestrator.findOrphanedSucceededInvocation(context.Background(),
				TaskRecord{ID: 1},
				[]TaskRecord{{ID: 2, Status: "retry_wait", Attempts: []AttemptRecord{{ID: 94, State: state}}}})
			if err != nil {
				t.Fatal(err)
			}
			if found {
				t.Fatalf("an attempt in %q was already decided and written down; treating its result as orphaned blocks a run that was recovering on its own", state)
			}
		})
	}
}

// The guard must still fire for the crash it exists for: an attempt that ended
// without any decision, leaving a durable result nobody claimed.
func TestAnUndecidedAttemptWithADurableResultIsStillAnOrphan(t *testing.T) {
	orchestrator, _ := newOrphanFixture("lease_expired")
	orphan, found, err := orchestrator.findOrphanedSucceededInvocation(context.Background(),
		TaskRecord{ID: 1},
		[]TaskRecord{{ID: 2, Status: "retry_wait", Attempts: []AttemptRecord{{ID: 94, State: "lease_expired"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("a lease that expired decided nothing, so a succeeded result behind it is genuinely unclaimed and must stop the run")
	}
	if orphan.InvocationID != 73 {
		t.Fatalf("orphan points at invocation %d", orphan.InvocationID)
	}
}

func newOrphanFixture(state string) (*Orchestrator, *fakeModels) {
	models := newFakeModels()
	models.invocations[invocationKey(2, 94)] = []InvocationRecord{{ID: 73, TaskID: 2, AttemptID: 94, Status: "succeeded"}}
	models.results[73] = InvocationResult{InvocationID: 73, JSONOutput: []byte(`{"ok":true}`)}
	return &Orchestrator{models: models}, models
}
