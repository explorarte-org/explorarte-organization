package executive

import (
	"context"
	"errors"
	"testing"
)

// Restart recovery rests on one fact: the opaque lease token is process-local
// and never persisted. A restarted process therefore cannot own any lease it
// finds durable, and no field on that lease can tell it otherwise.

// restartFixture is a root with one child holding an active lease whose token
// this process does not have -- the exact shape a process restart leaves
// behind.
type restartFixture struct {
	tasks   *memoryTasks
	models  *fakeModels
	harness *fakeHarness
	orch    *Orchestrator
	root    TaskRecord
	child   TaskRecord
}

func newRestartFixture(t *testing.T, holderID string) *restartFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	harness := newFakeHarness(models)
	orchestrator := testOrchestratorWithHarness(t, tasksPort, models, harness, &countingBudget{}, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-restart",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:restart",
		Requirements: []RequirementProposal{{Key: "executive_closure_verified", Type: "result", Description: "x", Required: true}},
	})
	child, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador",
		IdempotencyKey: childKey(root.ID, "ceo-plan"), Title: "child", Instructions: "child",
		AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	// A previous process claimed this attempt. The durable rows survive; the
	// plaintext token does not.
	child.Status = "running"
	child.Attempts = []AttemptRecord{{ID: 900, Ordinal: 1, State: "running"}}
	child.ActiveLease = &LeaseRecord{TaskID: child.ID, AttemptID: 900, HolderID: holderID}
	tasksPort.tasks[child.ID] = child
	return &restartFixture{tasks: tasksPort, models: models, harness: harness, orch: orchestrator, root: root, child: child}
}

// TestActiveLeaseWithoutLocalTokenIsInadoptableWhoeverHoldsIt is the §14
// regression guard. The old code inferred ownership from
// HolderID == "executive-orchestrator"; after the identity split the holder is
// a numeric principal, so that condition silently stopped matching and every
// prior-process lease would have looked adoptable. The holder value is now
// irrelevant, and this table proves it by varying exactly that field.
func TestActiveLeaseWithoutLocalTokenIsInadoptableWhoeverHoldsIt(t *testing.T) {
	for _, holder := range []string{
		orchestratorWorkerID, // the value the old inference keyed on
		"7001",               // a canonical role-bound principal, today's reality
		"some-other-worker",  // anything else
		"",                   // an unset holder
	} {
		t.Run("holder="+holder, func(t *testing.T) {
			fixture := newRestartFixture(t, holder)
			run, err := fixture.orch.ResumeDurable(context.Background(), fixture.root.ID)
			if !errors.Is(err, ErrRunBlocked) {
				t.Fatalf("err=%v want the active lease to be an impassable barrier", err)
			}
			if fixture.harness.callCount() != 0 {
				t.Fatalf("harness runs=%d: a restarted process executed beside an active lease", fixture.harness.callCount())
			}
			if len(fixture.tasks.claims) != 0 {
				t.Fatalf("claims=%d: a restarted process must not create a second attempt while the lease is active", len(fixture.tasks.claims))
			}
			if len(fixture.tasks.heartbeatActorLog()) != 0 {
				t.Fatal("a restarted process must never heartbeat a lease it cannot prove it holds")
			}
			current, _ := fixture.tasks.GetTask(context.Background(), fixture.child.ID)
			if current.Attempts[0].State != "running" || current.ActiveLease == nil {
				t.Fatalf("the barrier must leave the durable attempt untouched: %+v", current.Attempts)
			}
			if run.RootTaskID != fixture.root.ID {
				t.Fatalf("run=%+v", run)
			}
		})
	}
}

// TestLocalTokenLetsTheSameProcessContinue is the other half: possession of
// the token is what adoption means, so a process that has it is not blocked.
func TestLocalTokenLetsTheSameProcessContinue(t *testing.T) {
	fixture := newRestartFixture(t, "7001")
	// Same process: it still holds the opaque token it was issued at claim.
	fixture.orch.rememberLease(fixture.child.ID, LeaseRecord{
		TaskID: fixture.child.ID, AttemptID: 900, HolderID: "7001", LeaseToken: "lease-900",
	})
	// Stop the run at the execution boundary so this test asserts the barrier
	// decision and nothing downstream of it.
	fixture.harness.failure = HarnessFailureAuthorityUnavailable
	fixture.harness.invocationStatus = ""
	_, err := fixture.orch.ResumeDurable(context.Background(), fixture.root.ID)
	if errors.Is(err, ErrRunBlocked) {
		t.Fatal("a process holding the lease token must not be blocked by its own lease")
	}
	if !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("err=%v want the run to have reached execution", err)
	}
	if fixture.harness.callCount() != 1 {
		t.Fatalf("harness runs=%d want 1", fixture.harness.callCount())
	}
}

// TestUnadoptableAttemptWithAmbiguousInvocationBlocksAmbiguous: ambiguity is a
// fact about the provider, not about who holds the lease, and it is never
// resolved by trying again.
func TestUnadoptableAttemptWithAmbiguousInvocationBlocksAmbiguous(t *testing.T) {
	fixture := newRestartFixture(t, "7001")
	fixture.models.setInvocations(fixture.child.ID, 900, InvocationRecord{
		ID: 771, TaskID: fixture.child.ID, AttemptID: 900, Status: "ambiguous",
	})
	_, err := fixture.orch.ResumeDurable(context.Background(), fixture.root.ID)
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("err=%v want model_outcome_ambiguous", err)
	}
	root, _ := fixture.tasks.GetTask(context.Background(), fixture.root.ID)
	if root.Status != "blocked" || root.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("root=%+v", root)
	}
	if fixture.harness.callCount() != 0 {
		t.Fatalf("harness runs=%d: an ambiguous outcome was retried", fixture.harness.callCount())
	}
	// A second pass must stay blocked and still not execute.
	if _, second := fixture.orch.ResumeDurable(context.Background(), fixture.root.ID); !errors.Is(second, ErrModelOutcomeAmbiguous) {
		t.Fatalf("second resume err=%v", second)
	}
	if fixture.harness.callCount() != 0 {
		t.Fatalf("harness runs after second resume=%d want 0", fixture.harness.callCount())
	}
}

// TestDurableResultSurvivesRestartAndBecomesOrphanAfterExpiry covers the
// crash-after-provider-result case. While the lease is active the answer is
// "barrier": the process that owns it may still be recording the result, and
// declaring it orphaned would be a verdict on another process's work. Once the
// lease expires and reconciliation moves the attempt out of running, the
// durable result is recognised as orphaned instead of being recomputed.
func TestDurableResultSurvivesRestartAndBecomesOrphanAfterExpiry(t *testing.T) {
	fixture := newRestartFixture(t, "7001")
	fixture.models.setInvocations(fixture.child.ID, 900, InvocationRecord{
		ID: 772, TaskID: fixture.child.ID, AttemptID: 900, Status: "succeeded",
	})
	fixture.models.setResult(772, InvocationResult{InvocationID: 772, JSONOutput: defaultHarnessBody})

	_, err := fixture.orch.ResumeDurable(context.Background(), fixture.root.ID)
	if !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("err=%v want the active lease to remain a barrier", err)
	}
	if fixture.harness.callCount() != 0 {
		t.Fatalf("harness runs=%d: a durable result was recomputed", fixture.harness.callCount())
	}

	// Lease expiry + reconciliation: the attempt leaves running and the lease
	// is released. This is what the task engine does durably; here it is
	// applied to the in-memory rows so the next decision is exercised.
	child, _ := fixture.tasks.GetTask(context.Background(), fixture.child.ID)
	child.Status = "ready"
	child.ActiveLease = nil
	child.Attempts = []AttemptRecord{{ID: 900, Ordinal: 1, State: "lease_expired"}}
	fixture.tasks.tasks[child.ID] = child

	_, err = fixture.orch.ResumeDurable(context.Background(), fixture.root.ID)
	if !errors.Is(err, ErrOrphanedModelResult) {
		t.Fatalf("err=%v want orphaned_model_result", err)
	}
	root, _ := fixture.tasks.GetTask(context.Background(), fixture.root.ID)
	if root.Status != "blocked" || root.ReasonCode != "orphaned_model_result" {
		t.Fatalf("root=%+v", root)
	}
	if fixture.harness.callCount() != 0 {
		t.Fatalf("harness runs=%d: an orphaned durable result must never be recomputed", fixture.harness.callCount())
	}
}
