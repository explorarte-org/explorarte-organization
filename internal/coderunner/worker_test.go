package coderunner

import (
	"context"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"testing"
	"time"
)

type queueFake struct {
	claim                         tasks.ClaimRequest
	started, heartbeats, recorded int
}

func (q *queueFake) ClaimTasks(_ context.Context, t tasks.ClaimRequest) ([]tasks.ClaimedTask, error) {
	q.claim = tasks.ClaimRequest{WorkerID: t.WorkerID, HolderPrincipalID: t.HolderPrincipalID, AssignedRoleID: t.AssignedRoleID}
	return []tasks.ClaimedTask{{Task: tasks.Task{ID: 1, AssignedRoleID: RoleID, Instructions: `{"schema_version":"code-runner-execution/v1","operations":[{"type":"GIT_STATUS"}]}`}, Attempt: tasks.Attempt{ID: 2}, Lease: tasks.Lease{HolderID: t.HolderPrincipalID}, LeaseToken: "opaque"}}, nil
}
func (q *queueFake) StartAttempt(context.Context, tasks.LeaseCommand) (tasks.Task, error) {
	q.started++
	return tasks.Task{}, nil
}
func (q *queueFake) Heartbeat(context.Context, tasks.LeaseCommand) (tasks.Lease, error) {
	q.heartbeats++
	return tasks.Lease{}, nil
}
func (q *queueFake) RecordAttemptResult(context.Context, tasks.RecordAttemptResultCommand) (tasks.Task, error) {
	q.recorded++
	return tasks.Task{}, nil
}

type execFake struct{}

func (execFake) Execute(context.Context, Plan) ([]Result, error) {
	return []Result{{Type: GitStatus, Success: true}}, nil
}
func TestWorkerFiltersRoleAndUsesPrincipal(t *testing.T) {
	q := &queueFake{}
	w := Worker{Queue: q, Executor: execFake{}, WorkerID: "runner-1", HolderPrincipalID: "42", LeaseDuration: time.Second}
	n, e := w.RunOnce(context.Background())
	if e != nil || n != 1 || q.claim.AssignedRoleID != RoleID || q.claim.WorkerID != "runner-1" || q.claim.HolderPrincipalID != "42" || q.started != 1 || q.recorded != 1 {
		t.Fatalf("n=%d err=%v q=%+v", n, e, q)
	}
}
