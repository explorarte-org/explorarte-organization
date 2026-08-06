// Package cellworker implements a persistent, restart-safe process that
// polls for model invocations this execution principal is eligible to
// dispatch and calls Dispatch on each. It holds no local durable state:
// eligibility, claims, and dispatch quota are all enforced server-side
// (Ramas 08-11), so recovery after a crash or restart is just calling Run
// again — any invocation this process had in flight is either still
// unclaimed (never left "requested"/"claimed") or was already claimed and
// will be picked up again by whichever principal reconciliation covers it.
// This package never selects a provider, holds credentials, or renders
// context; those stay behind the Dispatcher and WorkSource ports.
package cellworker

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// Worker runs the poll/dispatch loop described in the package doc.
type Worker struct {
	cfg        Config
	work       WorkSource
	dispatcher Dispatcher
	clock      Clock
}

// New validates cfg and wires a Worker against the given ports. clock may be
// nil, in which case SystemClock is used.
func New(cfg Config, work WorkSource, dispatcher Dispatcher, clock Clock) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if work == nil || dispatcher == nil {
		return nil, ErrInvalidConfig
	}
	if clock == nil {
		clock = SystemClock
	}
	return &Worker{cfg: cfg, work: work, dispatcher: dispatcher, clock: clock}, nil
}

// Run polls for eligible work and dispatches it until ctx is cancelled. It
// always returns ctx.Err() once every in-flight Dispatch call has finished
// (or hit its ShutdownGrace deadline) — Run never returns while a Dispatch
// call it started is still running. Run is not safe to call concurrently on
// the same Worker; a crashed or exited Run may always be restarted by
// calling Run again, including from a freshly constructed Worker.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.cfg.Concurrency)
	b := newBackoff(w.cfg.MinBackoff, w.cfg.MaxBackoff, rand.New(rand.NewSource(time.Now().UnixNano())))

	dispatchOne := func(invocationID int64) {
		defer wg.Done()
		defer func() { <-sem }()
		dispatchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.ShutdownGrace)
		defer cancel()
		_, _ = w.dispatcher.Dispatch(dispatchCtx, invocationID)
	}

pollLoop:
	for {
		select {
		case <-ctx.Done():
			break pollLoop
		default:
		}

		ids, err := w.work.ListEligible(ctx, w.cfg.PrincipalKey, w.cfg.BatchSize)
		if err != nil || len(ids) == 0 {
			if !w.clock.Sleep(ctx, b.Next()) {
				break pollLoop
			}
			continue
		}
		b.Reset()

		for _, id := range ids {
			select {
			case <-ctx.Done():
				break pollLoop
			case sem <- struct{}{}:
			}
			wg.Add(1)
			go dispatchOne(id)
		}
	}

	wg.Wait()
	return ctx.Err()
}
