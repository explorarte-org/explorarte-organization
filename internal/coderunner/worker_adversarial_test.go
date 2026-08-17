package coderunner

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// blockingExec simulates an in-flight execution that only returns once
// release is closed, so tests can assert exactly what worker.run does while
// an execution is still outstanding.
type blockingExec struct {
	started  chan struct{}
	release  chan struct{}
	returned int32
}

func newBlockingExec() *blockingExec {
	return &blockingExec{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingExec) Execute(ctx context.Context, _ Plan) ([]Result, error) {
	close(b.started)
	<-b.release
	atomic.StoreInt32(&b.returned, 1)
	return nil, ctx.Err()
}

// TestWorkerJoinsBeforeReturningOnParentCancellation proves worker.run never
// returns while the execution it started might still be mutating the
// workspace: cancelling the parent context must not make RunOnce return
// until the executor goroutine actually does.
func TestWorkerJoinsBeforeReturningOnParentCancellation(t *testing.T) {
	q := &queueFake{}
	exec := newBlockingExec()
	w := Worker{Queue: q, Executor: exec, Workspace: workspaceFake{}, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: time.Hour, ShutdownGrace: 3 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = w.RunOnce(ctx)
		close(done)
	}()
	<-exec.started
	cancel()
	select {
	case <-done:
		t.Fatal("worker returned before the execution it started had joined")
	case <-time.After(200 * time.Millisecond):
	}
	if atomic.LoadInt32(&exec.returned) != 0 {
		t.Fatal("test invariant broken: executor already returned")
	}
	close(exec.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never returned after the execution released")
	}
}

// TestWorkerReportsIndeterminateWhenJoinExceedsGrace proves the fail-closed
// contract: if the bounded join itself elapses without the executor
// returning, worker.run reports ErrIndeterminateExecution rather than
// waiting forever or silently succeeding.
func TestWorkerReportsIndeterminateWhenJoinExceedsGrace(t *testing.T) {
	q := &queueFake{}
	exec := newBlockingExec() // release is never closed
	w := Worker{Queue: q, Executor: exec, Workspace: workspaceFake{}, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: time.Hour, ShutdownGrace: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := w.RunOnce(ctx)
		errCh <- err
	}()
	<-exec.started
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrIndeterminateExecution) {
			t.Fatalf("want ErrIndeterminateExecution, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not return within a bounded time")
	}
}

// TestWorkerJoinsOnHeartbeatLoss proves lease loss follows the same bounded
// join as parent cancellation, instead of returning immediately while the
// executor might still be running.
func TestWorkerJoinsOnHeartbeatLoss(t *testing.T) {
	q := &queueFake{heartbeatErr: errors.New("lease lost upstream")}
	exec := newBlockingExec()
	w := Worker{Queue: q, Executor: exec, Workspace: workspaceFake{}, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: 80 * time.Millisecond, ShutdownGrace: 3 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		_, err := w.RunOnce(context.Background())
		errCh <- err
	}()
	<-exec.started
	select {
	case err := <-errCh:
		t.Fatalf("worker returned before joining execution: %v", err)
	case <-time.After(400 * time.Millisecond):
		// heartbeat has had time to fire and fail; join must still be holding.
	}
	close(exec.release)
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "lease lost") {
			t.Fatalf("want lease-lost error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not return after release")
	}
}

func TestVerifyOrderingRejectsMutationAfterVerification(t *testing.T) {
	plan := Plan{Operations: []Operation{{Type: ApplyPatch, Patch: "x"}, {Type: GoTest}, {Type: ApplyPatch, Patch: "y"}}}
	results := []Result{{Type: ApplyPatch, Success: true}, {Type: GoTest, Success: true}, {Type: ApplyPatch, Success: true}}
	if err := verifyOrdering(plan, results); err == nil {
		t.Fatal("expected stale verification to be rejected")
	}
}

func TestVerifyOrderingAllowsCheckAfterMutation(t *testing.T) {
	plan := Plan{Operations: []Operation{{Type: ApplyPatch, Patch: "x"}, {Type: GoTest}}}
	results := []Result{{Type: ApplyPatch, Success: true}, {Type: GoTest, Success: true}}
	if err := verifyOrdering(plan, results); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyOrderingAllowsPlansWithNoChecks(t *testing.T) {
	plan := Plan{Operations: []Operation{{Type: GitStatus}}}
	if err := verifyOrdering(plan, []Result{{Type: GitStatus, Success: true}}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkerBlocksSuccessWhenEvidencePersistenceFails proves "no success
// without durable evidence": if RecordEvidence fails, the attempt is
// reported as a failure, never as succeeded.
func TestWorkerBlocksSuccessWhenEvidencePersistenceFails(t *testing.T) {
	q := &queueFake{evidenceErr: errors.New("evidence store unavailable")}
	w := Worker{Queue: q, Executor: execFake{}, Workspace: workspaceFake{}, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: time.Second}
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if q.lastResult.Outcome != tasks.OutcomeNonRetryableFailure || q.lastResult.FailureCode != "evidence_persistence_failed" {
		t.Fatalf("outcome=%v code=%v", q.lastResult.Outcome, q.lastResult.FailureCode)
	}
}

// TestWorkerDeniesWrongRoleAssignment proves a task claimed under the wrong
// assigned role is still denied at the CodeRunner boundary even if a queue
// implementation somehow returned one.
func TestWorkerDeniesWrongRoleAssignment(t *testing.T) {
	q := &queueFake{claimOverride: &tasks.ClaimedTask{
		Task:       tasks.Task{ID: 1, AssignedRoleID: "empresa/other-role", Instructions: `{"schema_version":"code-runner-execution/v1","operations":[{"type":"GIT_STATUS"}]}`},
		Attempt:    tasks.Attempt{ID: 2},
		Lease:      tasks.Lease{HolderID: "42"},
		LeaseToken: "opaque",
	}}
	w := Worker{Queue: q, Executor: execFake{}, Workspace: workspaceFake{}, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: time.Second}
	_, err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected denial for wrong assigned role")
	}
	if q.started != 0 || q.recorded != 0 || q.evidenced != 0 {
		t.Fatalf("denied task must never start/record/evidence: started=%d recorded=%d evidenced=%d", q.started, q.recorded, q.evidenced)
	}
}

// TestWorkerDeniesPrincipalMismatch proves a lease holder that does not
// match this worker's own canonical principal is denied even though the
// role assignment is correct.
func TestWorkerDeniesPrincipalMismatch(t *testing.T) {
	q := &queueFake{claimOverride: &tasks.ClaimedTask{
		Task:       tasks.Task{ID: 1, AssignedRoleID: RoleID, Instructions: `{"schema_version":"code-runner-execution/v1","operations":[{"type":"GIT_STATUS"}]}`},
		Attempt:    tasks.Attempt{ID: 2},
		Lease:      tasks.Lease{HolderID: "someone-else"},
		LeaseToken: "opaque",
	}}
	w := Worker{Queue: q, Executor: execFake{}, Workspace: workspaceFake{}, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: time.Second}
	_, err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected denial for principal mismatch")
	}
	if q.started != 0 || q.recorded != 0 || q.evidenced != 0 {
		t.Fatalf("denied task must never start/record/evidence: started=%d recorded=%d evidenced=%d", q.started, q.recorded, q.evidenced)
	}
}

// TestWorkerClassifiesIndeterminateExecutorErrorAsNonRetryable proves that
// an executor-level indeterminate result (process-tree termination could not
// be proven) is recorded as a non-retryable failure with the dedicated
// failure code, never as success, and the workspace is never sealed.
func TestWorkerClassifiesIndeterminateExecutorErrorAsNonRetryable(t *testing.T) {
	q := &queueFake{}
	sealed := 0
	ws := sealTrackingWorkspace{onSeal: func() { sealed++ }}
	exec := indeterminateExec{}
	w := Worker{Queue: q, Executor: exec, Workspace: ws, WorkerID: "r", HolderPrincipalID: "42", LeaseDuration: time.Second}
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if q.lastResult.Outcome != tasks.OutcomeNonRetryableFailure || q.lastResult.FailureCode != "indeterminate_code_execution" {
		t.Fatalf("outcome=%v code=%v", q.lastResult.Outcome, q.lastResult.FailureCode)
	}
	if sealed != 0 {
		t.Fatal("an indeterminate execution must never seal the workspace")
	}
}

type indeterminateExec struct{}

func (indeterminateExec) Execute(context.Context, Plan) ([]Result, error) {
	return nil, ErrIndeterminateExecution
}

type sealTrackingWorkspace struct {
	onSeal func()
}

func (sealTrackingWorkspace) Open(context.Context, tasks.ClaimedTask, string) (string, int64, error) {
	return "/tmp/runner", 1, nil
}
func (w sealTrackingWorkspace) Seal(ctx context.Context, id int64, item tasks.ClaimedTask, actor string) (staging.Workspace, error) {
	w.onSeal()
	return staging.Workspace{}, nil
}
