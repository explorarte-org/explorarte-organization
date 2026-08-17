package runtimeadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type captureTasks struct {
	executive.TaskCoordinator
	created executive.CreateTaskCommand
	listed  []executive.TaskRecord
}

func (c *captureTasks) CreateTask(_ context.Context, command executive.CreateTaskCommand) (executive.TaskRecord, bool, error) {
	c.created = command
	return executive.TaskRecord{ID: 99, IdempotencyKey: command.IdempotencyKey, CorrelationID: command.CorrelationID}, false, nil
}
func (c *captureTasks) ListByCorrelation(context.Context, string) ([]executive.TaskRecord, error) {
	return append([]executive.TaskRecord(nil), c.listed...), nil
}

type fakeBudgetModels struct {
	executive.ModelInvocationReader
	byAttempt map[[2]int64][]executive.InvocationRecord
}

func (f *fakeBudgetModels) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]executive.InvocationRecord, error) {
	return append([]executive.InvocationRecord(nil), f.byAttempt[[2]int64{taskID, attemptID}]...), nil
}

type passCompletion struct{}

func (passCompletion) Verify(context.Context, int64, int64) (executive.CompletionResult, error) {
	return executive.CompletionResult{Verdict: executive.CompletionPass}, nil
}

type evidenceModels struct {
	executive.ModelInvocationReader
	invocation executive.InvocationRecord
	result     executive.InvocationResult
}

func (f evidenceModels) FindTaskAttemptInvocations(context.Context, int64, int64) ([]executive.InvocationRecord, error) {
	return []executive.InvocationRecord{f.invocation}, nil
}
func (f evidenceModels) GetResult(context.Context, int64) (executive.InvocationResult, error) {
	return f.result, nil
}

func TestDAGTasksAddsSourceDependencyAtCreate(t *testing.T) {
	capture := &captureTasks{}
	guard := DAGTasks{TaskCoordinator: capture}
	_, _, err := guard.CreateTask(context.Background(), executive.CreateTaskCommand{
		IdempotencyKey: "executive:1:worker:ingenieria_ia:a",
		CausationID:    "task:42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.created.Dependencies) != 1 || capture.created.Dependencies[0] != 42 {
		t.Fatalf("dependencies=%v", capture.created.Dependencies)
	}
}

// TestModelCallBudgetRejectsProspectiveThirdCEOCall is the mutation guard for
// MaxModelCalls after the Harness migration: the gate is consulted before the
// run, from durable state, and refuses the call that would exceed the budget.
func TestModelCallBudgetRejectsProspectiveThirdCEOCall(t *testing.T) {
	tasks := &captureTasks{listed: []executive.TaskRecord{{
		ID: 1, AssignedRoleID: executive.CEORoleID, IdempotencyKey: "executive:1:ceo-plan", CorrelationID: "executive:x",
		Attempts: []executive.AttemptRecord{{ID: 11}, {ID: 12}, {ID: 13}},
	}}}
	models := &fakeBudgetModels{byAttempt: map[[2]int64][]executive.InvocationRecord{
		{1, 11}: []executive.InvocationRecord{{ID: 101}},
		{1, 12}: []executive.InvocationRecord{{ID: 102}},
	}}
	guard := ModelCallBudget{Models: models, Tasks: tasks, Limits: executive.DefaultLimits()}
	err := guard.AuthorizeModelCall(context.Background(), executive.ModelCallBudgetRequest{TaskID: 1, AttemptID: 13, CorrelationID: "executive:x"})
	if !errors.Is(err, executive.ErrBudgetExceeded) {
		t.Fatalf("err=%v", err)
	}
}

// TestModelCallBudgetDoesNotChargeAResumedAttempt: an attempt that already has
// a durable invocation is the same logical model call, so resuming it is free.
// Charging it again would make a restart look like new spend and could exhaust
// a budget purely by retrying.
func TestModelCallBudgetDoesNotChargeAResumedAttempt(t *testing.T) {
	tasks := &captureTasks{listed: []executive.TaskRecord{{
		ID: 1, AssignedRoleID: executive.CEORoleID, IdempotencyKey: "executive:1:ceo-plan", CorrelationID: "executive:x",
		Attempts: []executive.AttemptRecord{{ID: 11}, {ID: 12}, {ID: 13}},
	}}}
	models := &fakeBudgetModels{byAttempt: map[[2]int64][]executive.InvocationRecord{
		{1, 11}: {{ID: 101}},
		{1, 12}: {{ID: 102}},
		{1, 13}: {{ID: 103}},
	}}
	guard := ModelCallBudget{Models: models, Tasks: tasks, Limits: executive.DefaultLimits()}
	if err := guard.AuthorizeModelCall(context.Background(), executive.ModelCallBudgetRequest{TaskID: 1, AttemptID: 13, CorrelationID: "executive:x"}); err != nil {
		t.Fatalf("resuming an attempt that already has its invocation must not be charged again: %v", err)
	}
}

func TestEvidenceProjectionCarriesValidatedWorkerResultAndEvidence(t *testing.T) {
	body := []byte("{\"schema_version\":\"worker-result/v1\",\"summary\":\"actual bounded worker result\",\"evidence_refs\":[\"artifact:abc\",\"check:def\"]}")
	adapter := EvidenceTasks{
		Models: evidenceModels{
			invocation: executive.InvocationRecord{ID: 77, Status: "succeeded"},
			result:     executive.InvocationResult{InvocationID: 77, JSONOutput: body, ResponseHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		Completion: passCompletion{}, Limits: executive.DefaultLimits(),
	}
	worker, err := adapter.projectWorker(context.Background(), executive.TaskRecord{
		ID: 9, AssignedRoleID: "ingenieria_ia/qa", Status: "completed",
		Attempts: []executive.AttemptRecord{{ID: 5, Ordinal: 1, State: "finished"}},
		Evidence: []executive.EvidenceRecord{{Reference: "model-invocation:77"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if worker.Summary != "actual bounded worker result" || worker.Completion != executive.CompletionPass {
		t.Fatalf("worker=%+v", worker)
	}
	if len(worker.EvidenceRefs) != 2 || len(worker.TaskEvidence) != 1 {
		t.Fatalf("worker evidence=%+v", worker)
	}
}
