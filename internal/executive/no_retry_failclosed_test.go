package executive

import (
	"context"
	"errors"
	"testing"
)

func TestNoRetriesBlocksRootWhenPhaseErrorWouldOtherwiseBeNonBlocking(t *testing.T) {
	tasks := newMemoryTasks()
	root, _, err := tasks.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID:  OwnerRoleID,
		AssignedRoleID:     CEORoleID,
		TaskClass:          TaskClassOwnerGoal,
		IdempotencyKey:     "no-retry-fail-closed-root",
		Title:              "root",
		Instructions:       "root",
		AcceptanceCriteria: []string{"bounded"},
		CorrelationID:      "executive:no-retry-fail-closed",
	})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := &Orchestrator{tasks: tasks, noRetries: true}
	_, gotErr := orchestrator.handlePhaseError(context.Background(), root, TaskRecord{}, ErrModelResultContractRejected)
	if !errors.Is(gotErr, ErrModelResultContractRejected) {
		t.Fatalf("error=%v, want original contract rejection", gotErr)
	}
	blocked, err := tasks.GetTask(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != "blocked" || blocked.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("root=%+v, want blocked with model_result_contract_rejected", blocked)
	}
}
