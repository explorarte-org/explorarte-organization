package main

import (
	"context"
	"errors"
	"fmt"
)

// executiveDriverWorkerName and financeReviewWorkerName name the two
// required long-running workers of `orgctl executive worker run` in every
// error superviseExecutiveWorkers produces, so a failure always says WHICH
// of the two died.
const (
	executiveDriverWorkerName = "campaign driver"
	financeReviewWorkerName   = "finance worker"
)

// workerOutcome pairs one supervised worker's name with its terminal
// error (nil only when its own context was already cancelled when it
// returned -- see run below).
type workerOutcome struct {
	name string
	err  error
}

// superviseExecutiveWorkers runs the two required, long-running workers of
// the executive worker process -- executiveRun (the autonomous
// CampaignDriver) and financeRun (the autonomous finance review worker) --
// as a single fail-fast unit under a shared child context derived from
// ctx.
//
// Both workers are expected to run until their context is cancelled.
// Anything else -- a real error, or even a "successful" nil return before
// that -- is treated as that worker's unexpected termination: a required
// worker silently going away while the process otherwise looks alive is
// worse than a visible, restart-recoverable process crash (this process
// runs under systemd Restart=always).
//
// Behavior:
//   - The first worker to terminate is observed before either goroutine is
//     joined (no Wait-before-observe).
//   - If ctx is still active at that moment, the termination is
//     unexpected: the sibling's context is cancelled immediately, BEFORE
//     waiting for the sibling to exit, and the ORIGINAL error (never the
//     sibling's resulting context.Canceled) is what gets returned.
//   - If ctx is already done, this is normal shutdown: both workers are
//     expected to return nil (or context.Canceled) as they unwind, and
//     that unwinding is not itself an error.
//   - Both goroutines are always joined before this function returns --
//     no detached workers, no leaks, regardless of outcome.
func superviseExecutiveWorkers(ctx context.Context, executiveRun, financeRun func(context.Context) error) error {
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	results := make(chan workerOutcome, 2)
	run := func(name string, fn func(context.Context) error) {
		err := fn(workerCtx)
		if err == nil && workerCtx.Err() == nil {
			// This worker returned "successfully" before anything --
			// neither the parent shutting down nor a sibling failure --
			// ever cancelled its own context. For a long-lived required
			// worker that can only mean it silently stopped doing its
			// job: treated as a failure, never as "done", so a future
			// bug can never quietly disable Finance or Executive while
			// leaving the process itself looking healthy.
			err = fmt.Errorf("exited unexpectedly with no error")
		}
		results <- workerOutcome{name: name, err: err}
	}

	go run(executiveDriverWorkerName, executiveRun)
	go run(financeReviewWorkerName, financeRun)

	first := <-results
	if ctx.Err() == nil {
		// The parent process is still supposed to be running: this is a
		// real, unexpected termination. Cancel the sibling BEFORE
		// waiting for it below, so it never keeps running orphaned.
		workerCancel()
	}
	second := <-results

	return classifySupervisionOutcome(ctx, first, second)
}

// classifySupervisionOutcome turns the two observed worker outcomes into
// the single error superviseExecutiveWorkers returns (nil for a clean,
// fully-graceful shutdown).
func classifySupervisionOutcome(ctx context.Context, first, second workerOutcome) error {
	if ctx.Err() != nil {
		// Normal shutdown: both workers are expected to unwind via
		// context cancellation. A shutdown-triggered context.Canceled
		// from either side is not itself a failure, but a REAL error
		// surfacing during shutdown still is.
		if first.err != nil && !errors.Is(first.err, context.Canceled) {
			return fmt.Errorf("%s failed: %w", first.name, first.err)
		}
		if second.err != nil && !errors.Is(second.err, context.Canceled) {
			return fmt.Errorf("%s failed: %w", second.name, second.err)
		}
		return nil
	}

	// The parent context was still active when the FIRST worker
	// terminated -- that is the real, primary failure, and run() above
	// guarantees first.err is non-nil in this branch (a premature nil
	// return is itself synthesized into an error there). The second
	// worker's own result is very likely context.Canceled -- a direct
	// consequence of the workerCancel() this function's caller issued in
	// response to the first failure -- and must never be reported as the
	// primary cause, even if it happens to carry a "real looking" error
	// of its own.
	if first.err != nil {
		return fmt.Errorf("%s failed: %w", first.name, first.err)
	}
	return fmt.Errorf("%s and %s exited unexpectedly with no error", first.name, second.name)
}
