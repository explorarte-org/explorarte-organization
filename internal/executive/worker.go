package executive

import (
	"context"
	"errors"
	"time"
)

type RootSource interface {
	ListExecutableRoots(context.Context, int) ([]int64, error)
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
	cfg          WorkerConfig
}

func NewWorker(orchestrator *Orchestrator, roots RootSource, cfg WorkerConfig) (*Worker, error) {
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
	return &Worker{orchestrator: orchestrator, roots: roots, cfg: cfg}, nil
}

func (w *Worker) RunOnce(ctx context.Context) error {
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
