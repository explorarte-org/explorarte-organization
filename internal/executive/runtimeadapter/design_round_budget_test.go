package runtimeadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// Local smoke #34 (root 1773): one department replanned once in design round 1
// and once in design round 2 -- each within the orchestrator's per-round limit --
// and the call guard refused round 2's replan review with "replans", because it
// counted both against one. The shape bounds scale with the rounds planned.
func TestTheCallGuardAllowsOneReplanPerDesignRound(t *testing.T) {
	const dept = "ingenieria_ia"
	keys := []string{
		"executive:1773:leader-plan:" + dept,
		"executive:1773:leader-review:" + dept,
		"executive:1773:leader-review:" + dept + ":replan:1",
		"executive:1773:leader-plan:" + dept + ":design-round:2",
		"executive:1773:leader-review:" + dept + ":design-round:2",
		"executive:1773:leader-review:" + dept + ":design-round:2:replan:1",
	}
	listed := []executive.TaskRecord{}
	invocations := map[[2]int64][]executive.InvocationRecord{}
	for i, key := range keys {
		id := int64(i + 1)
		task := executive.TaskRecord{ID: id, AssignedRoleID: dept + "/orquestador", IdempotencyKey: key, CorrelationID: "executive:x",
			Attempts: []executive.AttemptRecord{{ID: 100 + id}}}
		if i < len(keys)-1 {
			invocations[[2]int64{id, 100 + id}] = []executive.InvocationRecord{{ID: 1000 + id}}
		}
		listed = append(listed, task)
	}
	guard := ModelCallBudget{Models: &fakeBudgetModels{byAttempt: invocations}, Tasks: &captureTasks{listed: listed}, Limits: executive.DefaultLimits()}
	last := int64(len(keys))
	if err := guard.AuthorizeModelCall(context.Background(), executive.ModelCallBudgetRequest{TaskID: last, AttemptID: 100 + last, CorrelationID: "executive:x"}); err != nil {
		t.Fatalf("round 2's replan review was refused: %v", err)
	}

	// A second replan inside one round is still beyond the shape.
	listed = append(listed, executive.TaskRecord{ID: 7, AssignedRoleID: dept + "/orquestador", CorrelationID: "executive:x",
		IdempotencyKey: "executive:1773:leader-review:" + dept + ":design-round:2:replan:2", Attempts: []executive.AttemptRecord{{ID: 107}}})
	listed = append(listed, executive.TaskRecord{ID: 8, AssignedRoleID: dept + "/orquestador", CorrelationID: "executive:x",
		IdempotencyKey: "executive:1773:leader-review:" + dept + ":replan:2", Attempts: []executive.AttemptRecord{{ID: 108}}})
	guard.Tasks = &captureTasks{listed: listed}
	if err := guard.AuthorizeModelCall(context.Background(), executive.ModelCallBudgetRequest{TaskID: 8, AttemptID: 108, CorrelationID: "executive:x"}); !errors.Is(err, executive.ErrBudgetExceeded) {
		t.Fatalf("four replans in two rounds: err = %v, want ErrBudgetExceeded", err)
	}
}
