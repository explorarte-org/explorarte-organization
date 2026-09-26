package financeworker

import (
	"context"
	"errors"
	"testing"
)

// Audit 2026-09-26, finding 6: a review request whose task ended without a review must not stay
// pending forever. The worker reconciles at the start of every sweep, and a reconciliation failure
// never keeps a ready review from running.

type fakeRequestReconciler struct {
	calls  int
	orgs   []string
	failed []int64
	err    error
}

func (f *fakeRequestReconciler) FailReviewRequestsOfTerminalTasks(_ context.Context, organizationID string, limit int) ([]int64, error) {
	f.calls++
	f.orgs = append(f.orgs, organizationID)
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	return f.failed, f.err
}

func TestEverySweepReconcilesRequestsOfTerminalTasks(t *testing.T) {
	reconciler := &fakeRequestReconciler{failed: []int64{1, 2, 27, 30}}
	exec := newFakeExecutor(nil)
	w := newTestWorker(t, []int64{5}, reqFor(5), exec, DefaultConfig("org"), WithRequestReconciler(reconciler))

	metrics, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reconciler.calls != 1 || reconciler.orgs[0] != "org" {
		t.Fatalf("reconciler calls=%d orgs=%v, want one call for the worker's organization", reconciler.calls, reconciler.orgs)
	}
	if metrics.RequestsFailed != 4 {
		t.Fatalf("RequestsFailed=%d, want 4", metrics.RequestsFailed)
	}
	if exec.callCount(5) != 1 {
		t.Fatal("the ready review did not run")
	}
	if _, err = w.RunOnce(context.Background()); err != nil || reconciler.calls != 2 {
		t.Fatalf("the second sweep did not reconcile: calls=%d err=%v", reconciler.calls, err)
	}
}

func TestAReconciliationFailureIsReportedAndDoesNotStopTheSweep(t *testing.T) {
	reconciler := &fakeRequestReconciler{err: errors.New("database unavailable")}
	exec := newFakeExecutor(nil)
	var reported []error
	w := newTestWorker(t, []int64{5}, reqFor(5), exec, DefaultConfig("org"), WithRequestReconciler(reconciler),
		WithObserver(func(_ int64, classification ResultClassification, err error) {
			if classification == ResultInfraFailure && err != nil {
				reported = append(reported, err)
			}
		}))

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("a reconciliation failure failed the sweep: %v", err)
	}
	if len(reported) != 1 || !errors.Is(reported[0], reconciler.err) {
		t.Fatalf("the reconciliation failure was not reported: %v", reported)
	}
	if exec.callCount(5) != 1 {
		t.Fatal("a reconciliation failure kept the ready review from running")
	}
}

func TestAWorkerWithoutAReconcilerStillRuns(t *testing.T) {
	exec := newFakeExecutor(nil)
	w := newTestWorker(t, []int64{5}, reqFor(5), exec, DefaultConfig("org"))
	if _, err := w.RunOnce(context.Background()); err != nil || exec.callCount(5) != 1 {
		t.Fatalf("err=%v calls=%d", err, exec.callCount(5))
	}
}
