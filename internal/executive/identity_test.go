package executive

import (
	"context"
	"errors"
	"testing"
)

// TestClaimIssuesLeaseToRoleBoundPrincipalNotWorkerID is the whole point of
// the identity split: the attempt records the operational worker, the lease is
// issued to the canonical role-bound principal, and the two values are not the
// same string. A regression that passed the worker name as the holder would
// still "work" against a permissive fake, which is why the fake enforces the
// same holder/actor rule PostgreSQL does.
func TestClaimIssuesLeaseToRoleBoundPrincipalNotWorkerID(t *testing.T) {
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	orchestrator := testOrchestratorForPorts(t, tasksPort, models, &fakeCompletion{verdict: CompletionPass})
	root, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID, IdempotencyKey: "root-identity",
		Title: "root", Instructions: "root", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:identity",
	})
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "child-identity",
		Title: "child", Instructions: "child", AcceptanceCriteria: []string{"x"}, CorrelationID: root.CorrelationID,
		Requirements: []RequirementProposal{{Key: "typed_plan", Type: "result", Description: "x", Required: true}},
	})
	if _, err := orchestrator.driveTypedTask(context.Background(), root, task, departmentPlanOutputSchema, PurposeDepartmentPlan, func(InvocationResult) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(tasksPort.claims) != 1 {
		t.Fatalf("claims=%d want 1", len(tasksPort.claims))
	}
	claim := tasksPort.claims[0]
	if claim.WorkerID != orchestratorWorkerID {
		t.Fatalf("worker_id=%q want %q", claim.WorkerID, orchestratorWorkerID)
	}
	if claim.HolderPrincipalID == "" || claim.HolderPrincipalID == claim.WorkerID {
		t.Fatalf("holder principal %q must be a canonical principal, never the worker name", claim.HolderPrincipalID)
	}
	if claim.AssignedRoleID != "ingenieria_ia/orquestador" {
		t.Fatalf("assigned role=%q", claim.AssignedRoleID)
	}
	current, _ := tasksPort.GetTask(context.Background(), task.ID)
	if len(current.Attempts) != 1 {
		t.Fatalf("attempts=%d want 1", len(current.Attempts))
	}
	if got := tasksPort.workerIDs[current.Attempts[0].ID]; got != orchestratorWorkerID {
		t.Fatalf("attempt worker_id=%q want %q", got, orchestratorWorkerID)
	}
}

// TestLeaseMutationsRejectTheWorkerNameAsActor is the mutation guard for the
// rule above: if anything downstream starts presenting the worker name where
// the lease holder is required, the task engine denies it. The fake reproduces
// the durable check (`task_leases.holder_id != ActorID -> ErrLeaseMismatch`),
// so this stays a real assertion and not an assumption about Postgres.
func TestLeaseMutationsRejectTheWorkerNameAsActor(t *testing.T) {
	tasksPort := newMemoryTasks()
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "actor-check",
		Title: "t", Instructions: "t", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:actor",
	})
	_, _, lease, err := tasksPort.ClaimTask(context.Background(), ClaimTaskCommand{
		TaskID: task.ID, WorkerID: orchestratorWorkerID, HolderPrincipalID: "4242",
		AssignedRoleID: "ingenieria_ia/orquestador", LeaseDuration: executiveLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasksPort.StartAttempt(context.Background(), lease, orchestratorWorkerID); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("start attempt as the worker name err=%v want lease mismatch", err)
	}
	if _, err := tasksPort.Heartbeat(context.Background(), lease, orchestratorWorkerID, executiveLeaseTTL); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("heartbeat as the worker name err=%v want lease mismatch", err)
	}
	if _, err := tasksPort.StartAttempt(context.Background(), lease, "4242"); err != nil {
		t.Fatalf("start attempt as the lease holder: %v", err)
	}
}

// TestClaimWithoutHolderPrincipalIsRejected proves the legacy
// tasks.ClaimRequest fallback (holder := WorkerID when HolderPrincipalID is
// empty) is not reachable from the Executive.
func TestClaimWithoutHolderPrincipalIsRejected(t *testing.T) {
	tasksPort := newMemoryTasks()
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "no-holder",
		Title: "t", Instructions: "t", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:no-holder",
	})
	if _, _, _, err := tasksPort.ClaimTask(context.Background(), ClaimTaskCommand{
		TaskID: task.ID, WorkerID: orchestratorWorkerID, AssignedRoleID: "ingenieria_ia/orquestador",
	}); err == nil {
		t.Fatal("a claim without a holder principal must be rejected")
	}
}
