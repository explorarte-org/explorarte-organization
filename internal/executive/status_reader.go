package executive

import (
	"context"
	"fmt"
)

// StatusTaskReader has no scheduling, lease or task-mutation authority.
type StatusTaskReader interface {
	GetTask(context.Context, int64) (TaskRecord, error)
	ListByCorrelation(context.Context, string) ([]TaskRecord, error)
}

// StatusResultReader reads already persisted model output; it cannot dispatch.
type StatusResultReader interface {
	FindTaskAttemptInvocations(context.Context, int64, int64) ([]InvocationRecord, error)
	GetResult(context.Context, int64) (InvocationResult, error)
}

// ReadStatus deliberately does not construct the execution runtime. A missing
// provider principal must not make a blocked campaign impossible to inspect.
func ReadStatus(ctx context.Context, tasks StatusTaskReader, models StatusResultReader, rootTaskID int64, limits Limits) (Run, error) {
	if tasks == nil || rootTaskID <= 0 {
		return Run{}, fmt.Errorf("%w: task reader and positive root id required", ErrInvalidInput)
	}
	root, err := tasks.GetTask(ctx, rootTaskID)
	if err != nil {
		return Run{}, err
	}
	if root.AssignedRoleID != CEORoleID || root.CorrelationID == "" {
		return Run{}, fmt.Errorf("%w: task is not an executive root", ErrInvalidInput)
	}
	children, err := tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	run := ProjectRun(root, withoutRoot(children, root.ID))
	if closure, ok := findTaskByMarker(children, keyClosureMarker); ok && closure.Status == "completed" {
		if result, ok := completedTaskResult(ctx, models, closure); ok {
			if parsed, err := ParseExecutiveClosure(result.JSONOutput, limits); err == nil {
				run.AnswerToOwner = parsed.AnswerToOwner
			}
		}
	}
	return run, nil
}

func completedTaskResult(ctx context.Context, models StatusResultReader, task TaskRecord) (InvocationResult, bool) {
	if models == nil {
		return InvocationResult{}, false
	}
	attemptID := latestFinishedAttemptID(task.Attempts)
	if attemptID == 0 {
		return InvocationResult{}, false
	}
	invocations, err := models.FindTaskAttemptInvocations(ctx, task.ID, attemptID)
	if err != nil || len(invocations) != 1 || invocations[0].Status != "succeeded" {
		return InvocationResult{}, false
	}
	result, err := models.GetResult(ctx, invocations[0].ID)
	return result, err == nil
}
