package executive

import (
	"context"
	"errors"
	"time"
)

type RootSource interface {
	ListExecutableRoots(context.Context, int) ([]int64, error)
}

// ExecutionReconciler recovers model executions whose provider outcome was
// never resolved -- an attempt that started sending and whose claim then
// expired, leaving the invocation in flight with nobody waiting for it.
//
// This worker's own error handling already ASSUMES someone does this. It
// skips ErrPriorExecutionUnresolved on the grounds that "an unresolved
// provider-side execution resolves itself once Model Runtime reconciles it",
// and skips ErrLeaseLost on the grounds that the attempt will be reconciled.
// Both were true of the code that could reconcile and false of the deployment
// that never ran it: the reconciler existed only as a CLI command nothing
// invoked, so a stranded invocation stayed stranded and every pass skipped it
// again for a recovery that was never coming.
//
// Running it here is what makes those comments true. It is optional so a
// deployment without Model Runtime is unaffected, and it is a port rather
// than a concrete dependency because reconciling model executions is Model
// Runtime's job, not the Executive's -- this worker only guarantees it
// happens on a schedule.
type ExecutionReconciler interface {
	Reconcile(ctx context.Context, batch int) error
}

type WorkerConfig struct {
	PollInterval time.Duration
	ErrorBackoff time.Duration
	BatchSize    int
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{PollInterval: time.Second, ErrorBackoff: 3 * time.Second, BatchSize: 16}
}

type Worker struct {
	orchestrator *Orchestrator
	roots        RootSource
	executions   ExecutionReconciler
	cfg          WorkerConfig
}

// WithExecutionReconciler runs model-execution reconciliation on every pass.
// Without it the worker behaves exactly as before, which is what a deployment
// with no Model Runtime needs.
func WithExecutionReconciler(reconciler ExecutionReconciler) WorkerOption {
	return func(w *Worker) {
		if reconciler != nil {
			w.executions = reconciler
		}
	}
}

// WorkerOption configures optional collaborators.
type WorkerOption func(*Worker)

func NewWorker(orchestrator *Orchestrator, roots RootSource, cfg WorkerConfig, options ...WorkerOption) (*Worker, error) {
	if orchestrator == nil || roots == nil {
		return nil, errors.New("executive worker requires orchestrator and root source")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.ErrorBackoff <= 0 {
		cfg.ErrorBackoff = 3 * time.Second
	}
	if cfg.BatchSize <= 0 || cfg.BatchSize > 128 {
		cfg.BatchSize = 16
	}
	worker := &Worker{orchestrator: orchestrator, roots: roots, cfg: cfg}
	for _, option := range options {
		option(worker)
	}
	return worker, nil
}

func (w *Worker) RunOnce(ctx context.Context) error {
	// Reconciliation runs BEFORE the roots are driven, so an execution that
	// resolved since the last pass is already settled when the run that waits
	// on it is resumed. Doing it afterwards would make every recovery cost an
	// extra poll interval for no reason.
	//
	// A reconciliation failure must not stop the pass. It is a recovery
	// sweep, not a precondition: refusing to drive any run because a cleanup
	// query failed would turn a transient database hiccup into a stalled
	// organization.
	if w.executions != nil {
		_ = w.executions.Reconcile(ctx, w.cfg.BatchSize)
	}
	rootIDs, err := w.roots.ListExecutableRoots(ctx, w.cfg.BatchSize)
	if err != nil {
		return err
	}
	for _, rootID := range rootIDs {
		_, runErr := w.orchestrator.ResumeDurable(ctx, rootID)
		if runErr == nil {
			continue
		}
		if errors.Is(runErr, ErrDispatchAssignmentRequired) ||
			errors.Is(runErr, ErrModelOutcomeAmbiguous) ||
			errors.Is(runErr, ErrIndeterminateToolExecution) ||
			errors.Is(runErr, ErrCompletionInconclusive) ||
			errors.Is(runErr, ErrRunBlocked) ||
			// The lease will expire, the task engine will reconcile the
			// attempt, and the next pass claims a fresh one. Nothing here has
			// to act on it.
			errors.Is(runErr, ErrLeaseLost) ||
			errors.Is(runErr, ErrExecutionAuthorityUnavailable) ||
			errors.Is(runErr, ErrExecutionPrincipalUnavailable) ||
			// An unresolved provider-side execution resolves itself once Model
			// Runtime reconciles it; the worker just comes back later.
			errors.Is(runErr, ErrPriorExecutionUnresolved) ||
			errors.Is(runErr, ErrExecutionInterrupted) {
			continue
		}
		// A single durable run failure must not terminate the process. The run
		// itself has already been blocked/failed by the orchestrator when safe.
	}
	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := w.RunOnce(ctx); err != nil {
			if !sleepContext(ctx, w.cfg.ErrorBackoff) {
				return nil
			}
			continue
		}
		if !sleepContext(ctx, w.cfg.PollInterval) {
			return nil
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
