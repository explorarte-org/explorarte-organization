package coderunner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const RoleID = "ingenieria_ia/code-runner"

// defaultShutdownGrace bounds how long worker.run waits, once cancellation
// has been requested (heartbeat/lease loss or parent shutdown), for the
// in-flight Executor.Execute call to actually return. It must be long
// enough to cover runSupervised's own killGrace for whatever operation is
// in flight, since the two bounds compose: the executor waits up to
// killGrace to confirm a process group died, and the worker waits up to
// this grace to see that outcome land on resultCh.
const defaultShutdownGrace = 30 * time.Second

type Queue interface {
	ClaimTasks(context.Context, tasks.ClaimRequest) ([]tasks.ClaimedTask, error)
	StartAttempt(context.Context, tasks.LeaseCommand) (tasks.Task, error)
	Heartbeat(context.Context, tasks.LeaseCommand) (tasks.Lease, error)
	RecordAttemptResult(context.Context, tasks.RecordAttemptResultCommand) (tasks.Task, error)
	RecordEvidence(context.Context, tasks.RecordEvidenceCommand) (tasks.Evidence, error)
}

type PlanExecutor interface {
	Execute(context.Context, Plan) ([]Result, error)
}
type WorkspacePort interface {
	Open(context.Context, tasks.ClaimedTask, string) (string, int64, error)
	Seal(context.Context, int64, tasks.ClaimedTask, string) (staging.Workspace, error)
}

type Worker struct {
	Queue             Queue
	Executor          PlanExecutor
	Workspace         WorkspacePort
	WorkerID          string
	HolderPrincipalID string
	LeaseDuration     time.Duration
	// ShutdownGrace overrides defaultShutdownGrace. Zero means the default.
	ShutdownGrace time.Duration
	// RuntimeVersion identifies the CodeRunner build/image and is recorded
	// as durable evidence for every succeeded attempt. Trusted deploy
	// metadata, never task input.
	RuntimeVersion string
}

func (w Worker) shutdownGrace() time.Duration {
	if w.ShutdownGrace <= 0 {
		return defaultShutdownGrace
	}
	return w.ShutdownGrace
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

type execOutcome struct {
	results []Result
	err     error
}

// run executes exactly one claimed task end to end. It never returns while
// the execution it started might still be mutating the staging workspace:
// every exit path (heartbeat loss, parent context cancellation, natural
// completion) joins the executor goroutine before returning, and the join
// itself is bounded (see awaitShutdown) so a stuck process cannot hang the
// worker forever -- it can only force an explicit indeterminate report.
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
		return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "invalid_execution_plan", err.Error())
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultCh := make(chan execOutcome, 1)
	go func() {
		r, e := w.Executor.Execute(execCtx, plan)
		resultCh <- execOutcome{r, e}
	}()

	ticker := time.NewTicker(w.LeaseDuration / 2)
	defer ticker.Stop()

	for {
		select {
		case out := <-resultCh:
			return w.finish(ctx, lease, workspaceID, item, plan, out.results, out.err)
		case <-ticker.C:
			if _, e := w.Queue.Heartbeat(ctx, lease); e != nil {
				cancel()
				return w.awaitShutdown(resultCh, fmt.Errorf("lease lost: %w", e))
			}
		case <-ctx.Done():
			cancel()
			return w.awaitShutdown(resultCh, ctx.Err())
		}
	}
}

// awaitShutdown is the bounded join used by every non-natural exit path.
// It never returns before the executor goroutine reports back, unless the
// bounded wait itself elapses -- in which case termination could not be
// proven and the caller learns that explicitly via ErrIndeterminateExecution
// instead of the worker silently walking away from a possibly-still-running
// process.
func (w Worker) awaitShutdown(resultCh <-chan execOutcome, triggerErr error) error {
	select {
	case <-resultCh:
		return triggerErr
	case <-time.After(w.shutdownGrace()):
		return fmt.Errorf("%v; execution could not be confirmed terminated within shutdown grace: %w", triggerErr, ErrIndeterminateExecution)
	}
}

// finish classifies a naturally-completed execution, enforces that any
// check used as proof of correctness post-dates the last mutation, seals
// the workspace, persists durable evidence, and only then reports success.
// If evidence persistence fails, the attempt is reported failed, never
// succeeded: Summary alone is not the evidence ledger.
func (w Worker) finish(ctx context.Context, lease tasks.LeaseCommand, workspaceID int64, item tasks.ClaimedTask, plan Plan, results []Result, execErr error) error {
	if execErr != nil {
		if errors.Is(execErr, ErrIndeterminateExecution) {
			return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "indeterminate_code_execution", execErr.Error())
		}
		return w.record(ctx, lease, tasks.OutcomeRetryableFailure, "execution_failed", execErr.Error())
	}
	if err := verifyOrdering(plan, results); err != nil {
		return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "stale_verification", err.Error())
	}
	sealed, err := w.Workspace.Seal(ctx, workspaceID, item, w.HolderPrincipalID)
	if err != nil {
		return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "seal_failed", err.Error())
	}
	env := detectEnvironment(ctx, w.RuntimeVersion)
	evidence := buildAttemptEvidence(item.Task.ID, item.Attempt.ID, plan.Operations, results, sealed, env)
	if err := recordAttemptEvidence(ctx, w.Queue, w.HolderPrincipalID, evidence); err != nil {
		return w.record(ctx, lease, tasks.OutcomeNonRetryableFailure, "evidence_persistence_failed", err.Error())
	}
	summary := fmt.Sprintf("code-runner attempt succeeded: %d operations, %d checks, sealed_workspace=%d", len(evidence.OperationsExecuted), len(evidence.ChecksRun), sealed.ID)
	return w.record(ctx, lease, tasks.OutcomeSucceeded, "", summary)
}

// verifyOrdering enforces that any mutation after a successful verification
// invalidates that verification: if a GO_BUILD/GO_VET/GO_TEST result is
// being used as proof of correctness for this attempt (i.e. it appears
// anywhere in the plan), the last such check must post-date the last
// mutating operation (APPLY_PATCH/GOFMT). A plan with no checks at all is
// not held to a requirement the existing contract never asked for.
func verifyOrdering(plan Plan, results []Result) error {
	lastMutation := 0
	lastCheck := 0
	for i, op := range plan.Operations {
		if i >= len(results) {
			break
		}
		ordinal := i + 1
		if op.Type.Mutates() {
			lastMutation = ordinal
		}
		if op.Type.isCheck() {
			lastCheck = ordinal
		}
	}
	if lastCheck == 0 {
		return nil
	}
	if lastCheck <= lastMutation {
		return fmt.Errorf("stale verification: last check at operation %d does not post-date last mutation at operation %d", lastCheck, lastMutation)
	}
	return nil
}

func (w Worker) record(ctx context.Context, l tasks.LeaseCommand, outcome tasks.AttemptOutcome, code, summary string) error {
	_, err := w.Queue.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{LeaseCommand: l, Result: tasks.AttemptResult{Outcome: outcome, FailureCode: code, Summary: summary}})
	return err
}
