package executive

import (
	"context"
	"testing"
)

type orphanModelCoordinator struct {
	ModelInvocationReader
	invocations map[[2]int64][]InvocationRecord
	results     map[int64]InvocationResult
}

func (f orphanModelCoordinator) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]InvocationRecord, error) {
	return append([]InvocationRecord(nil), f.invocations[[2]int64{taskID, attemptID}]...), nil
}
func (f orphanModelCoordinator) GetResult(_ context.Context, id int64) (InvocationResult, error) {
	return f.results[id], nil
}

func TestFindOrphanedSucceededInvocationBlocksReinferenceCandidate(t *testing.T) {
	models := orphanModelCoordinator{
		invocations: map[[2]int64][]InvocationRecord{
			{2, 20}: []InvocationRecord{{ID: 200, TaskID: 2, AttemptID: 20, Status: "succeeded"}},
		},
		results: map[int64]InvocationResult{
			200: {InvocationID: 200, JSONOutput: []byte("{\"schema_version\":\"worker-result/v1\",\"summary\":\"done\",\"evidence_refs\":[]}")},
		},
	}
	o := &Orchestrator{models: models}
	root := TaskRecord{ID: 1, CorrelationID: "executive:x"}
	children := []TaskRecord{{
		ID: 2, Status: "ready", Attempts: []AttemptRecord{{ID: 20, Ordinal: 1, State: "lease_expired"}},
	}}
	orphan, ok, err := o.findOrphanedSucceededInvocation(context.Background(), root, children)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || orphan.TaskID != 2 || orphan.AttemptID != 20 || orphan.InvocationID != 200 {
		t.Fatalf("orphan=%+v ok=%v", orphan, ok)
	}
}

func TestFindOrphanedSucceededInvocationIgnoresActiveRunningAttempt(t *testing.T) {
	models := orphanModelCoordinator{
		invocations: map[[2]int64][]InvocationRecord{
			{2, 20}: []InvocationRecord{{ID: 200, TaskID: 2, AttemptID: 20, Status: "succeeded"}},
		},
		results: map[int64]InvocationResult{200: {InvocationID: 200}},
	}
	o := &Orchestrator{models: models}
	_, ok, err := o.findOrphanedSucceededInvocation(context.Background(), TaskRecord{ID: 1}, []TaskRecord{{
		ID: 2, Status: "running", Attempts: []AttemptRecord{{ID: 20, State: "running"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("active running attempt must not be classified as orphaned")
	}
}
