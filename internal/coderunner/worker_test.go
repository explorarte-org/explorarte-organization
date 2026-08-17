package coderunner

import (
	"context"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"testing"
	"time"
)

type queueFake struct {
	claim                                    tasks.ClaimRequest
	instructions                             string
	started, heartbeats, recorded, evidenced int
	lastEvidence                             tasks.RecordEvidenceCommand
	lastResult                               tasks.AttemptResult
	heartbeatErr                             error
	evidenceErr                              error
	claimOverride                            *tasks.ClaimedTask
}

func (q *queueFake) ClaimTasks(_ context.Context, t tasks.ClaimRequest) ([]tasks.ClaimedTask, error) {
	q.claim = tasks.ClaimRequest{WorkerID: t.WorkerID, HolderPrincipalID: t.HolderPrincipalID, AssignedRoleID: t.AssignedRoleID}
	if q.claimOverride != nil {
		return []tasks.ClaimedTask{*q.claimOverride}, nil
	}
	plan := q.instructions
	if plan == "" {
		plan = `{"schema_version":"code-runner-execution/v1","operations":[{"type":"GIT_STATUS"}]}`
	}
	return []tasks.ClaimedTask{{Task: tasks.Task{ID: 1, AssignedRoleID: RoleID, Instructions: plan}, Attempt: tasks.Attempt{ID: 2}, Lease: tasks.Lease{HolderID: t.HolderPrincipalID}, LeaseToken: "opaque"}}, nil
}
func (q *queueFake) StartAttempt(context.Context, tasks.LeaseCommand) (tasks.Task, error) {
	q.started++
	return tasks.Task{}, nil
}
func (q *queueFake) Heartbeat(context.Context, tasks.LeaseCommand) (tasks.Lease, error) {
	q.heartbeats++
	if q.heartbeatErr != nil {
		return tasks.Lease{}, q.heartbeatErr
	}
	return tasks.Lease{}, nil
}
func (q *queueFake) RecordAttemptResult(_ context.Context, cmd tasks.RecordAttemptResultCommand) (tasks.Task, error) {
	q.recorded++
	q.lastResult = cmd.Result
	return tasks.Task{}, nil
}
func (q *queueFake) RecordEvidence(_ context.Context, cmd tasks.RecordEvidenceCommand) (tasks.Evidence, error) {
	q.evidenced++
	q.lastEvidence = cmd
	if q.evidenceErr != nil {
		return tasks.Evidence{}, q.evidenceErr
	}
	return tasks.Evidence{ID: 1, TaskID: cmd.TaskID}, nil
}

type execFake struct{}

func (execFake) Execute(context.Context, Plan) ([]Result, error) {
	return []Result{{Type: GitStatus, Success: true}}, nil
}

type workspaceFake struct{}

func (workspaceFake) Open(context.Context, tasks.ClaimedTask, string) (string, int64, error) {
	return "/tmp/runner", 1, nil
}
func (workspaceFake) Seal(context.Context, int64, tasks.ClaimedTask, string) (staging.Workspace, error) {
	return staging.Workspace{ID: 1, WorkspaceKey: "fake"}, nil
}
func TestWorkerFiltersRoleAndUsesPrincipal(t *testing.T) {
	q := &queueFake{}
	w := Worker{Queue: q, Executor: execFake{}, Workspace: workspaceFake{}, WorkerID: "runner-1", HolderPrincipalID: "42", LeaseDuration: time.Second}
	n, e := w.RunOnce(context.Background())
	if e != nil || n != 1 || q.claim.AssignedRoleID != RoleID || q.claim.WorkerID != "runner-1" || q.claim.HolderPrincipalID != "42" || q.started != 1 || q.recorded != 1 || q.evidenced != 1 {
		t.Fatalf("n=%d err=%v q=%+v", n, e, q)
	}
}
