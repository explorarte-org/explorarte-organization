package coderunner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type recordingQueue struct {
	order   *[]string
	claimed []tasks.ClaimedTask
	err     error
}

func (q recordingQueue) ClaimTasks(context.Context, tasks.ClaimRequest) ([]tasks.ClaimedTask, error) {
	*q.order = append(*q.order, "claim")
	return q.claimed, q.err
}
func (q recordingQueue) StartAttempt(context.Context, tasks.LeaseCommand) (tasks.Task, error) {
	panic("not reached")
}
func (q recordingQueue) Heartbeat(context.Context, tasks.LeaseCommand) (tasks.Lease, error) {
	panic("not reached")
}
func (q recordingQueue) RecordAttemptResult(context.Context, tasks.RecordAttemptResultCommand) (tasks.Task, error) {
	panic("not reached")
}
func (q recordingQueue) RecordEvidence(context.Context, tasks.RecordEvidenceCommand) (tasks.Evidence, error) {
	panic("not reached")
}

type recordingReconciler struct {
	order  *[]string
	batch  int
	err    error
	called int
}

func (r *recordingReconciler) Reconcile(_ context.Context, batch int) (tasks.ReconcileResult, error) {
	*r.order = append(*r.order, "reconcile")
	r.batch = batch
	r.called++
	return tasks.ReconcileResult{}, r.err
}

func newTestWorker(order *[]string, reconciler QueueReconciler) Worker {
	return Worker{
		Queue: recordingQueue{order: order}, Executor: &Executor{},
		WorkerID: "runner", HolderPrincipalID: "42", LeaseDuration: time.Second,
		Reconciler: reconciler,
	}
}

// A mission's retries were hostage to somebody else's work. Nothing but the
// Executive reconciled the queue, and only while driving one of its own
// roots, so a mission whose root had already finished sat in retry_wait
// forever with attempts remaining that could never be taken. max_attempts was
// fiction for exactly the missions most likely to need it.
//
// The order matters: reconciling after the claim would leave a mission whose
// delay has just elapsed unclaimable for one more pass.
func TestTheRunnerReconcilesBeforeItLooksForWork(t *testing.T) {
	var order []string
	reconciler := &recordingReconciler{order: &order}
	worker := newTestWorker(&order, reconciler)

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "reconcile" || order[1] != "claim" {
		t.Fatalf("the queue must be reconciled before it is read, got %v", order)
	}
	if reconciler.batch != reconcileBatch {
		t.Fatalf("batch=%d want %d", reconciler.batch, reconcileBatch)
	}
}

// A recovery sweep is not a precondition. Refusing to claim work because a
// cleanup query failed would turn a transient database hiccup into a stalled
// runner -- the same reason the Executive's worker ignores its own sweep's
// failure.
func TestAFailedSweepDoesNotStopThePass(t *testing.T) {
	var order []string
	reconciler := &recordingReconciler{order: &order, err: errors.New("connection refused")}
	worker := newTestWorker(&order, reconciler)

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("a failed sweep must not stop the pass: %v", err)
	}
	if len(order) != 2 || order[1] != "claim" {
		t.Fatalf("the worker must still look for work, got %v", order)
	}
}

// Optional means optional: a deployment whose queue somebody else reconciles
// must behave exactly as before.
func TestWithoutAReconcilerTheWorkerBehavesAsBefore(t *testing.T) {
	var order []string
	worker := newTestWorker(&order, nil)

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "claim" {
		t.Fatalf("no sweep should have run, got %v", order)
	}
}
