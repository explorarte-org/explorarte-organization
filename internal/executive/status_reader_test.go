package executive

import (
	"context"
	"errors"
	"testing"
)

type statusTasks struct {
	root     TaskRecord
	children []TaskRecord
	err      error
}

func (s statusTasks) GetTask(context.Context, int64) (TaskRecord, error) { return s.root, s.err }
func (s statusTasks) ListByCorrelation(context.Context, string) ([]TaskRecord, error) {
	return s.children, s.err
}

type noStatusModels struct{}

func (noStatusModels) FindTaskAttemptInvocations(context.Context, int64, int64) ([]InvocationRecord, error) {
	panic("blocked status must not read model results")
}
func (noStatusModels) GetResult(context.Context, int64) (InvocationResult, error) {
	panic("blocked status must not read model results")
}

func TestReadStatusNeedsNoExecutionPrincipal(t *testing.T) {
	// These readers cannot execute, authorize, mutate or bootstrap a provider.
	root := TaskRecord{ID: 19076, AssignedRoleID: CEORoleID, CorrelationID: "smoke", Status: "blocked", ReasonCode: "design_rounds_exhausted"}
	run, err := ReadStatus(context.Background(), statusTasks{root: root}, noStatusModels{}, root.ID, DefaultLimits())
	if err != nil || run.State != StateBlocked || run.ReasonCode != "design_rounds_exhausted" {
		t.Fatalf("%+v %v", run, err)
	}
}

func TestReadStatusRejectsNonRootAndPropagatesReadFailure(t *testing.T) {
	for _, root := range []TaskRecord{{ID: 1, AssignedRoleID: "ingenieria_ia/qa", CorrelationID: "x"}, {ID: 1, AssignedRoleID: CEORoleID}} {
		if _, err := ReadStatus(context.Background(), statusTasks{root: root}, noStatusModels{}, 1, DefaultLimits()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("non root: %v", err)
		}
	}
	want := errors.New("database unavailable")
	if _, err := ReadStatus(context.Background(), statusTasks{err: want}, noStatusModels{}, 1, DefaultLimits()); !errors.Is(err, want) {
		t.Fatal(err)
	}
}
