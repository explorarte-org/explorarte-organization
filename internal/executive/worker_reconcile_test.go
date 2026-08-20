package executive

import (
	"context"
	"errors"
	"testing"
)

// The worker skips unresolved provider-side executions because it expects
// them to settle on their own. That expectation is only true if something
// actually sweeps them, and for the whole of AUTONOMY-SMOKE-001 nothing did:
// the reconciler existed as a CLI command nobody ran, so invocation 62 sat in
// send_started while every pass skipped it again for a recovery that was
// never coming.
func TestEveryPassReconcilesUnresolvedExecutions(t *testing.T) {
	reconciler := &countingReconciler{}
	worker := newTestWorker(t, &emptyRoots{}, WithExecutionReconciler(reconciler))

	for pass := 1; pass <= 3; pass++ {
		if err := worker.RunOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if reconciler.calls != 3 {
		t.Fatalf("reconciled %d times in 3 passes: a sweep that runs once is a sweep that stops working the moment it is needed twice", reconciler.calls)
	}
	if reconciler.batch != DefaultWorkerConfig().BatchSize {
		t.Fatalf("batch=%d: the sweep must be bounded by the same batch the pass is", reconciler.batch)
	}
}

// A cleanup sweep is not a precondition for doing work. Refusing to drive any
// run because a recovery query failed would turn a transient database problem
// into a stalled organization.
func TestAReconciliationFailureDoesNotStopThePass(t *testing.T) {
	roots := &recordingRoots{ids: []int64{1, 2}}
	worker := newTestWorker(t, roots, WithExecutionReconciler(&countingReconciler{err: errors.New("database unavailable")}))

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("the pass must continue: %v", err)
	}
	if roots.calls != 1 {
		t.Fatal("the roots were never listed, so a failed sweep stopped the organization from working")
	}
}

// A deployment without Model Runtime configures no reconciler and must behave
// exactly as before.
func TestNoReconcilerIsNotAFailure(t *testing.T) {
	roots := &recordingRoots{}
	worker := newTestWorker(t, roots)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("a worker with no reconciler must run normally: %v", err)
	}
	if roots.calls != 1 {
		t.Fatal("the pass did not happen")
	}
}

func newTestWorker(t *testing.T, roots RootSource, options ...WorkerOption) *Worker {
	t.Helper()
	worker, err := NewWorker(&Orchestrator{}, roots, DefaultWorkerConfig(), options...)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

type countingReconciler struct {
	calls int
	batch int
	err   error
}

func (c *countingReconciler) Reconcile(_ context.Context, batch int) error {
	c.calls++
	c.batch = batch
	return c.err
}

type emptyRoots struct{}

func (emptyRoots) ListExecutableRoots(context.Context, int) ([]int64, error) { return nil, nil }

type recordingRoots struct {
	ids   []int64
	calls int
}

func (r *recordingRoots) ListExecutableRoots(context.Context, int) ([]int64, error) {
	r.calls++
	// The roots themselves are not driven here: this file is about the sweep
	// happening, and ResumeDurable needs a real orchestrator.
	return nil, nil
}
