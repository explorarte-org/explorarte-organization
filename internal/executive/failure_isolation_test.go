package executive

import "testing"

// A failure belongs to the fiber that had it. It must not take its siblings'
// phase down with it.
//
// The department used to be held open until every worker reached completed or
// no_action, so one failed or dead-lettered worker stalled the phase forever:
// the review that would have judged the department never ran, and the run
// waited on a task that was never coming back.
func TestAFailedWorkerDoesNotHoldItsSiblingsPhaseOpen(t *testing.T) {
	const root, dept = int64(1), "ingenieria_ia"
	for _, state := range []string{"failed", "dead_letter", "rejected", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			all := []TaskRecord{
				workerTask(root, dept, 2, "completed"),
				workerTask(root, dept, 3, state),
			}
			if !allDepartmentWorkersTerminal(all, root, dept) {
				t.Fatalf("a worker in %q has finished; holding the phase open denies the review the chance to judge it", state)
			}
		})
	}
}

// A worker that has not finished still holds the phase, and the distinction is
// the whole point: retry_wait is coming back, and blocked is asking for a
// human. Neither is a finished worker.
func TestAnUnfinishedWorkerStillHoldsThePhase(t *testing.T) {
	const root, dept = int64(1), "ingenieria_ia"
	for _, state := range []string{"retry_wait", "blocked", "running", "leased", "ready", "pending"} {
		t.Run(state, func(t *testing.T) {
			all := []TaskRecord{
				workerTask(root, dept, 2, "completed"),
				workerTask(root, dept, 3, state),
			}
			if allDepartmentWorkersTerminal(all, root, dept) {
				t.Fatalf("a worker in %q has not finished, so the phase must wait for it", state)
			}
		})
	}
}

func workerTask(root int64, dept string, id int64, status string) TaskRecord {
	return TaskRecord{
		ID: id, Status: status, AssignedRoleID: dept + "/qa",
		IdempotencyKey: childKey(root, "worker:"+dept+":w"+string(rune('0'+id))),
	}
}
