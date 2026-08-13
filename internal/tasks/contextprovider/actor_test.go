package contextprovider

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// ORG-AUDIT-010 regression: GetTaskContext/ValidateVersion must reject a
// task_ref whose AssignedRoleID differs from the requesting/expected actor.
// Before this fix neither method compared them at all -- the docstring on
// ValidateVersion claimed "the caller has already bound the task to the
// actor," which was never actually true of any caller in this codebase.

type fakeTaskReader struct{ detail tasks.TaskDetail }

func (f fakeTaskReader) GetTask(context.Context, int64) (tasks.TaskDetail, error) {
	return f.detail, nil
}
func (f fakeTaskReader) ListTasks(context.Context, tasks.TaskFilter) ([]tasks.Task, error) {
	return nil, nil
}
func (f fakeTaskReader) ListEvents(context.Context, int64, int) ([]tasks.Event, error) {
	return nil, nil
}
func (f fakeTaskReader) ListAttempts(context.Context, int64) ([]tasks.Attempt, error) {
	return nil, nil
}
func (f fakeTaskReader) ListDeadLetters(context.Context, int) ([]tasks.DeadLetter, error) {
	return nil, nil
}
func (f fakeTaskReader) GetDeadLetter(context.Context, int64) (tasks.DeadLetter, error) {
	return tasks.DeadLetter{}, nil
}

func taskDetailFixture() tasks.TaskDetail {
	return tasks.TaskDetail{Task: tasks.Task{
		ID: 42, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: "ingenieria_ia/orquestador", AssignedUnitID: "ingenieria_ia",
		Title: "assignee-check fixture", Instructions: "n/a", Status: tasks.StatusReady,
		Version: 1, RequestHash: "hash",
	}}
}

func TestGetTaskContextRejectsActorThatIsNotTheAssignee(t *testing.T) {
	provider, err := New(fakeTaskReader{detail: taskDetailFixture()})
	if err != nil {
		t.Fatal(err)
	}
	request := contextengine.BuildRequest{
		OrganizationID: "explorarte", OrganizationRevisionID: 7,
		ActorRoleID: "ingenieria_ia/qa", // not the assignee
		TaskRef:     "task:42",
	}
	if _, err := provider.GetTaskContext(context.Background(), request); err == nil {
		t.Fatal("expected GetTaskContext to reject an actor that is not the task assignee, got nil error")
	}
}

func TestGetTaskContextAllowsTheRealAssignee(t *testing.T) {
	provider, err := New(fakeTaskReader{detail: taskDetailFixture()})
	if err != nil {
		t.Fatal(err)
	}
	request := contextengine.BuildRequest{
		OrganizationID: "explorarte", OrganizationRevisionID: 7,
		ActorRoleID: "ingenieria_ia/orquestador", // the real assignee
		TaskRef:     "task:42",
	}
	if _, err := provider.GetTaskContext(context.Background(), request); err != nil {
		t.Fatalf("expected the real assignee to build task context, got err=%v", err)
	}
}

func TestValidateVersionRejectsActorThatIsNotTheAssignee(t *testing.T) {
	detail := taskDetailFixture()
	provider, err := New(fakeTaskReader{detail: detail})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := sourceRecord(detail)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.ValidateVersion(context.Background(), "ingenieria_ia/qa", expected); err == nil {
		t.Fatal("expected ValidateVersion to reject an actor that is not the task assignee, got nil error")
	}
	if err := provider.ValidateVersion(context.Background(), "ingenieria_ia/orquestador", expected); err != nil {
		t.Fatalf("expected the real assignee to revalidate cleanly, got err=%v", err)
	}
}
