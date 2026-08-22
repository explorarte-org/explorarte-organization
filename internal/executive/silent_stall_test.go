package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-013 stopped and said nothing.
//
// Its round-2 department review exhausted five attempts on a deterministic
// contract rejection and dead-lettered. driveTypedTask has branches for
// completed, awaiting_verification, ready, leased and running; a dead-lettered
// task matches none of them and falls through to "return task, nil". So every
// pass of the worker did nothing, returned no error, and logged nothing --
// the worker only logs failures. The root stayed ready for eleven minutes with
// no recorded reason, and the only signal that anything was wrong was the
// absence of signals.
//
// The projector already reported the run as failed the moment the child died.
// Nothing read that projection. This is the test for reading it.
func newStalledRunFixture(t *testing.T, childStatus string) (*Orchestrator, *memoryTasks, int64) {
	t.Helper()
	tasksPort := newMemoryTasks()
	const rootID int64 = 1
	const childID int64 = 2
	tasksPort.tasks[rootID] = TaskRecord{
		ID: rootID, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: CEORoleID, TaskClass: TaskClassOwnerGoal,
		CorrelationID: "executive:stalled", Status: "ready", MaxAttempts: 2,
	}
	tasksPort.tasks[childID] = TaskRecord{
		ID: childID, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: "ingenieria_ia/orquestador", TaskClass: "coordination.department_review",
		CorrelationID: "executive:stalled", Status: childStatus,
		ReasonCode: "model_result_contract_rejected", CausationID: "task:1",
	}
	orchestrator := &Orchestrator{
		tasks:    tasksPort,
		registry: fakeRegistry{rev: RevisionRef{ID: 7}},
	}
	return orchestrator, tasksPort, rootID
}

func TestARunStopsWhenAChildEndsWithoutItsResult(t *testing.T) {
	for _, childStatus := range []string{"dead_letter", "failed", "rejected"} {
		t.Run(childStatus, func(t *testing.T) {
			orchestrator, tasksPort, rootID := newStalledRunFixture(t, childStatus)

			if _, err := orchestrator.Resume(context.Background(), rootID); err != nil {
				t.Fatalf("resume: %v", err)
			}
			root := tasksPort.tasks[rootID]
			if root.Status != "blocked" {
				t.Fatalf("root is %q: a run whose child ended terminally must not sit ready forever", root.Status)
			}
			if root.ReasonCode != ReasonRunChildFailed {
				t.Fatalf("reason_code=%q, want %q", root.ReasonCode, ReasonRunChildFailed)
			}
			// The reason has to name what ended the run. "Something failed"
			// is the same silence in a different shape.
			if !strings.Contains(root.Reason, "2") || !strings.Contains(root.Reason, childStatus) {
				t.Fatalf("reason must name the task and how it ended, got %q", root.Reason)
			}
			if !strings.Contains(root.Reason, "model_result_contract_rejected") {
				t.Fatalf("reason must carry the child's own reason code, got %q", root.Reason)
			}
		})
	}
}

// Reopening it would walk straight back into the pass that found the dead
// child and block again, one model call poorer every time it re-drove whatever
// came before it. This is the same bound ReasonDesignRoundsExhausted needed.
func TestAStoppedRunIsNotSilentlyReopened(t *testing.T) {
	orchestrator, tasksPort, rootID := newStalledRunFixture(t, "dead_letter")
	if _, err := orchestrator.Resume(context.Background(), rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.Resume(context.Background(), rootID); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("a stopped run must stay stopped, got %v", err)
	}
	if got := tasksPort.tasks[rootID].Status; got != "blocked" {
		t.Fatalf("root is %q after a second pass, want blocked", got)
	}
}

// A child that is merely waiting is not a child that failed. Blocking a run
// because one of its tasks is blocked would turn every request for a human
// into the end of the campaign.
func TestARunIsNotStoppedByAChildThatIsMerelyWaiting(t *testing.T) {
	for _, childStatus := range []string{"blocked", "retry_wait", "ready", "running", "completed"} {
		t.Run(childStatus, func(t *testing.T) {
			if isFailedTask(childStatus) {
				t.Fatalf("%q must not count as a terminal failure", childStatus)
			}
		})
	}
}
