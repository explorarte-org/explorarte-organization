package executive

import "testing"

// TestProjectRunReportsCapacityWaitAsBlockedCharacterization freezes the
// CURRENTLY OBSERVED behavior for CAPACITY_EXHAUSTION_SCHEDULING_V1's FASE
// E audit: a worker durably blocked with status_reason_code=capacity (a
// self-resolving, Task-Engine-owned wait -- see
// internal/tasks/postgres/reconcile.go's reconcileTaskReadiness) is
// reported by ProjectRun exactly the same way as every other blocked
// reason: Run.State=StateBlocked, with Run.ReasonCode carrying the
// distinguishing "capacity" value.
//
// This is deliberately a characterization test, not a correctness test.
// The round's own audit found no production consumer that treats
// Run.State==StateBlocked as a stop signal for autonomous reconciliation:
// internal/executive/runtimeadapter/roots.go's ListExecutableRoots (which
// decides what the persistent Worker in internal/executive/worker.go even
// attempts) and internal/executive/recovery.go's ResumeDurable (which
// decides whether to act once it does) both gate on the ROOT TASK's own
// durable tasks.Status, never on this projection -- confirmed empirically
// by every capacity-wait integration test in this package, where the root
// task's own status stays "ready" throughout the wait. Collapsing
// "capacity" into the same StateBlocked label as "needs a human
// intervention" is a reporting-precision gap (internal/executive/
// status_reader.go's ReadStatus is explicitly a read-only inspection
// surface for CLI/API consumers, never wired to any scheduling decision),
// not a functional autonomy bug -- so this round does not change RunState.
//
// If that audit's conclusion ever changes, this test is the trip wire:
// touching it should force re-reading why.
func TestProjectRunReportsCapacityWaitAsBlockedCharacterization(t *testing.T) {
	const root = int64(1)
	children := []TaskRecord{
		{
			ID: 10, AssignedRoleID: "ingenieria_ia/qa", Status: "blocked",
			ReasonCode: "capacity", Reason: "transient_no_capacity: every pool candidate is temporarily unavailable",
			IdempotencyKey: childKey(root, "worker:ingenieria_ia:a"),
		},
	}
	run := ProjectRun(TaskRecord{ID: root, Status: "ready", CorrelationID: "executive:characterization-test"}, children)
	if run.State != StateBlocked {
		t.Fatalf("PROJECT_RUN_CAPACITY_WAIT characterization: expected ProjectRun to currently report StateBlocked for a capacity-waiting worker, got %q -- if this changed, re-read the FASE E audit before touching it", run.State)
	}
	if run.ReasonCode != "capacity" {
		t.Fatalf("PROJECT_RUN_CAPACITY_WAIT characterization: Run.ReasonCode must carry the distinguishing capacity reason even though State collapses to blocked, got %q", run.ReasonCode)
	}
}
