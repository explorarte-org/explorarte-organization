package coderunner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const RoleID = "ingenieria_ia/code-runner"

type Queue interface {
	ClaimTasks(context.Context, tasks.ClaimRequest) ([]tasks.ClaimedTask, error)
	StartAttempt(context.Context, tasks.LeaseCommand) (tasks.Task, error)
	Heartbeat(context.Context, tasks.LeaseCommand) (tasks.Lease, error)
	RecordAttemptResult(context.Context, tasks.RecordAttemptResultCommand) (tasks.Task, error)
}

type PlanExecutor interface {
	Execute(context.Context, Plan) ([]Result, error)
}
type WorkspacePort interface {
	Open(context.Context, tasks.ClaimedTask, string) (string, int64, error)
	Seal(context.Context, int64, tasks.ClaimedTask, string) (any, error)
}

type Worker struct {
	Queue             Queue
	Executor          PlanExecutor
	Workspace         WorkspacePort
	WorkerID          string
	HolderPrincipalID string
	LeaseDuration     time.Duration
}

func (w Worker) RunOnce(ctx context.Context) (int, error) {
	if w.Queue == nil || w.Executor == nil || w.WorkerID == "" || w.HolderPrincipalID == "" || w.LeaseDuration <= 0 {
		return 0, fmt.Errorf("invalid code-runner worker")
	}
	claimed, err := w.Queue.ClaimTasks(ctx, tasks.ClaimRequest{WorkerID: w.WorkerID, HolderPrincipalID: w.HolderPrincipalID, AssignedRoleID: RoleID, BatchSize: 1, LeaseDuration: w.LeaseDuration})
	if err != nil {
		return 0, err
	}
	for _, item := range claimed {
		if err := w.run(ctx, item); err != nil {
			return 1, err
		}
	}
	return len(claimed), nil
}

func (w Worker) run(ctx context.Context, item tasks.ClaimedTask) error {
	if item.Task.AssignedRoleID != RoleID || item.Lease.HolderID != w.HolderPrincipalID {
		return fmt.Errorf("code-runner assignment or principal mismatch")
	}
	lease := tasks.LeaseCommand{TaskID: item.Task.ID, AttemptID: item.Attempt.ID, LeaseToken: item.LeaseToken, ActorID: w.HolderPrincipalID, Extension: w.LeaseDuration}
	if _, err := w.Queue.StartAttempt(ctx, lease); err != nil {
		return err
	}
	if w.Workspace == nil {
		return fmt.Errorf("code-runner workspace boundary is required")
	}
	path, workspaceID, err := w.Workspace.Open(ctx, item, w.HolderPrincipalID)
	if err != nil {
		return err
	}
	if setter, ok := w.Executor.(interface{ SetWorkspace(string) }); ok {
		setter.SetWorkspace(path)
	}
	plan, err := ParsePlan([]byte(item.Task.Instructions))
	if err != nil {
		return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "invalid_execution_plan", err)
	}
	resultCh := make(chan struct {
		results []Result
		err     error
	}, 1)
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		r, e := w.Executor.Execute(execCtx, plan)
		resultCh <- struct {
			results []Result
			err     error
		}{r, e}
	}()
	ticker := time.NewTicker(w.LeaseDuration / 2)
	defer ticker.Stop()
	for {
		select {
		case out := <-resultCh:
			if out.err != nil {
				return w.record(ctx, lease, tasks.OutcomeRetryableFailure, "execution_failed", out.err)
			}
			sealed, e := w.Workspace.Seal(ctx, workspaceID, item, w.HolderPrincipalID)
			if e != nil {
				return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "seal_failed", e)
			}
			return w.record(ctx, lease, tasks.OutcomeSucceeded, "", map[string]any{"operations": out.results, "sealed": sealed})
		case <-ticker.C:
			if _, e := w.Queue.Heartbeat(ctx, lease); e != nil {
				cancel()
				<-resultCh
				return fmt.Errorf("lease lost: %w", e)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (w Worker) record(ctx context.Context, l tasks.LeaseCommand, outcome tasks.AttemptOutcome, code string, summary any) error {
	s := fmt.Sprint(summary)
	_, err := w.Queue.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{LeaseCommand: l, Result: tasks.AttemptResult{Outcome: outcome, FailureCode: code, Summary: s}})
	return err
}
func jsonSummary(v any) string { b, _ := json.Marshal(v); return string(b) }
