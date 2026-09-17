package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// blockUntilDone is the standard "long-lived required worker" test double
// used throughout: it blocks until its context is cancelled, then returns
// whatever result was configured for that case (nil, unless the test
// specifically wants to exercise the return-value-at-shutdown path).
func blockUntilDone(result error) func(context.Context) error {
	return func(ctx context.Context) error {
		<-ctx.Done()
		return result
	}
}

// runSupervisorWithTimeout fails the test if superviseExecutiveWorkers
// does not return within the given bound -- every mandatory test below is
// specifically about the supervisor returning promptly, so a hang here
// must fail loudly rather than exhaust the whole test binary's timeout.
func runSupervisorWithTimeout(t *testing.T, ctx context.Context, executiveRun, financeRun func(context.Context) error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- superviseExecutiveWorkers(ctx, executiveRun, financeRun) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("superviseExecutiveWorkers did not return within 5s")
		return nil
	}
}

// MANDATORY TEST A -- FINANCE FAILS: the campaign worker blocks until
// ctx.Done() then exits; the finance worker returns a real error
// immediately, with no external parent cancellation. The supervisor must
// return promptly, the campaign worker must observe cancellation (proven
// by the fact the supervisor returns at all -- blockUntilDone only exits
// once its context is cancelled), and the returned error must contain the
// finance failure.
func TestSuperviseExecutiveWorkers_FinanceFails(t *testing.T) {
	ctx := context.Background()
	financeErr := errors.New("finance boom")

	err := runSupervisorWithTimeout(t, ctx, blockUntilDone(nil), func(context.Context) error {
		return financeErr
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "finance boom") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "finance boom")
	}
	if !strings.Contains(err.Error(), financeReviewWorkerName) {
		t.Errorf("error = %q, want it to name %q", err.Error(), financeReviewWorkerName)
	}
}

// MANDATORY TEST B -- EXECUTIVE FAILS: the inverse of Test A. The
// campaign driver returns a fatal error, the finance worker blocks. The
// finance worker must receive cancellation, and the supervisor must
// return the fatal campaign error.
func TestSuperviseExecutiveWorkers_ExecutiveFails(t *testing.T) {
	ctx := context.Background()
	executiveErr := errors.New("campaign boom")

	err := runSupervisorWithTimeout(t, ctx, func(context.Context) error {
		return executiveErr
	}, blockUntilDone(nil))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "campaign boom") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "campaign boom")
	}
	if !strings.Contains(err.Error(), executiveDriverWorkerName) {
		t.Errorf("error = %q, want it to name %q", err.Error(), executiveDriverWorkerName)
	}
}

// MANDATORY TEST C -- UNEXPECTED NIL: finance returns nil while the
// parent context is still active (no external cancellation, and nothing
// else has failed). This is not a healthy exit for a long-lived required
// worker: the sibling must be cancelled and the supervisor must return an
// error. The symmetric case (executive returns an unexpected nil) is
// proven immediately after, since the implementation allows it cheaply.
func TestSuperviseExecutiveWorkers_FinanceUnexpectedNil(t *testing.T) {
	ctx := context.Background()

	err := runSupervisorWithTimeout(t, ctx, blockUntilDone(nil), func(context.Context) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected an error for an unexpected nil exit, got nil")
	}
	if !strings.Contains(err.Error(), financeReviewWorkerName) {
		t.Errorf("error = %q, want it to name %q", err.Error(), financeReviewWorkerName)
	}
}

func TestSuperviseExecutiveWorkers_ExecutiveUnexpectedNil(t *testing.T) {
	ctx := context.Background()

	err := runSupervisorWithTimeout(t, ctx, func(context.Context) error {
		return nil
	}, blockUntilDone(nil))
	if err == nil {
		t.Fatal("expected an error for an unexpected nil exit, got nil")
	}
	if !strings.Contains(err.Error(), executiveDriverWorkerName) {
		t.Errorf("error = %q, want it to name %q", err.Error(), executiveDriverWorkerName)
	}
}

// MANDATORY TEST D -- NORMAL PARENT SHUTDOWN: both workers block on
// context; the test cancels the parent. Both must observe cancellation
// and the supervisor must return nil (runExecutiveWorker's exitOK path).
func TestSuperviseExecutiveWorkers_NormalParentShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- superviseExecutiveWorkers(ctx, blockUntilDone(nil), blockUntilDone(nil)) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil on normal parent shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("superviseExecutiveWorkers did not return within 5s of parent cancellation")
	}
}

// MANDATORY TEST E -- BOTH FAIL NEARLY TOGETHER: no deadlock, no
// goroutine leak, one deterministic primary error is returned. Exact
// ordering is not guaranteed (and this test does not assert it), only
// that the supervisor returns promptly with a real error naming exactly
// one of the two failures. Both workers are released simultaneously via
// an already-closed channel, so there is no ordering dependency between
// test setup and the supervised goroutines.
func TestSuperviseExecutiveWorkers_BothFailConcurrently(t *testing.T) {
	ctx := context.Background()
	executiveErr := errors.New("campaign boom")
	financeErr := errors.New("finance boom")
	start := make(chan struct{})
	close(start)

	err := runSupervisorWithTimeout(t, ctx, func(context.Context) error {
		<-start
		return executiveErr
	}, func(context.Context) error {
		<-start
		return financeErr
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	sawExecutive := strings.Contains(err.Error(), "campaign boom")
	sawFinance := strings.Contains(err.Error(), "finance boom")
	if sawExecutive == sawFinance {
		t.Errorf("error = %q, want it to name exactly one of the two failures", err.Error())
	}
}

// MANDATORY TEST F -- SIBLING CANCELLATION ERROR: finance fails with a
// real error; the campaign driver, once cancelled in response, returns
// context.Canceled (not nil) -- exercising the case where the sibling's
// own return value looks like an error too. The primary result must
// still be the finance failure, never "context canceled".
func TestSuperviseExecutiveWorkers_SiblingCancellationDoesNotHidePrimaryFailure(t *testing.T) {
	ctx := context.Background()
	financeErr := errors.New("finance boom")

	err := runSupervisorWithTimeout(t, ctx, func(runCtx context.Context) error {
		<-runCtx.Done()
		return context.Canceled
	}, func(context.Context) error {
		return financeErr
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "finance boom") {
		t.Errorf("error = %q, want it to contain the primary failure %q", err.Error(), "finance boom")
	}
	if strings.Contains(err.Error(), "context canceled") && !strings.Contains(err.Error(), "finance boom") {
		t.Errorf("error = %q, sibling's context.Canceled must never stand in for the primary failure", err.Error())
	}
}

// GOROUTINE LEAK TEST: after superviseExecutiveWorkers returns, both
// worker goroutines must have exited -- proven by observing that each
// worker function's body has actually run to completion (not merely that
// the channel it sends on was read), using a counter each worker
// increments only after it is fully done, checked shortly after return.
func TestSuperviseExecutiveWorkers_NoGoroutineLeak(t *testing.T) {
	ctx := context.Background()
	var executiveDone, financeDone atomic.Bool

	err := runSupervisorWithTimeout(t, ctx, func(runCtx context.Context) error {
		<-runCtx.Done()
		executiveDone.Store(true)
		return nil
	}, func(context.Context) error {
		defer financeDone.Store(true)
		return errors.New("finance boom")
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	// superviseExecutiveWorkers only returns after reading BOTH results
	// from its internal channel, and each goroutine sends on that channel
	// only after its run() wrapper's call to fn has returned -- so by the
	// time runSupervisorWithTimeout's <-done fires, both flags below are
	// necessarily already set. No sleep, no polling: this is a direct
	// consequence of the supervisor's own join, asserted immediately.
	if !executiveDone.Load() {
		t.Error("campaign driver goroutine did not complete before supervisor returned")
	}
	if !financeDone.Load() {
		t.Error("finance worker goroutine did not complete before supervisor returned")
	}
}

// classifySupervisionOutcome's ctx.Err()!=nil branch must not report a
// REAL error as if it were a graceful shutdown, even during normal parent
// cancellation -- proven directly against the classification function to
// pin its contract independent of goroutine timing.
func TestClassifySupervisionOutcome_RealErrorDuringShutdownStillSurfaces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := classifySupervisionOutcome(ctx,
		workerOutcome{name: executiveDriverWorkerName, err: errors.New("disk full")},
		workerOutcome{name: financeReviewWorkerName, err: context.Canceled},
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "disk full")
	}
}

func TestClassifySupervisionOutcome_GracefulShutdownReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := classifySupervisionOutcome(ctx,
		workerOutcome{name: executiveDriverWorkerName, err: nil},
		workerOutcome{name: financeReviewWorkerName, err: context.Canceled},
	)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWorkerOutcomeErrorFormatting(t *testing.T) {
	// Sanity check on the log-facing shape the round asks for:
	// "executive worker: finance worker failed: ..." /
	// "executive worker: campaign driver failed: ...".
	err := fmt.Errorf("%s failed: %w", financeReviewWorkerName, errors.New("finance boom"))
	if got := "executive worker: " + err.Error(); got != "executive worker: finance worker failed: finance boom" {
		t.Errorf("unexpected log shape: %q", got)
	}
}
