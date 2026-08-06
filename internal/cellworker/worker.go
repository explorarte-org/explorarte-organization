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
	observer   Observer
}

// New validates cfg and wires a Worker against the given ports. clock may be
// nil, in which case SystemClock is used. observer may be nil, in which
// case list/dispatch errors are discarded (matching prior behavior).
func New(cfg Config, work WorkSource, dispatcher Dispatcher, clock Clock, observer Observer) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if work == nil || dispatcher == nil {
		return nil, ErrInvalidConfig
	}
	if clock == nil {
		clock = SystemClock
	}
	if observer == nil {
		observer = NoopObserver{}
	}
	return &Worker{cfg: cfg, work: work, dispatcher: dispatcher, clock: clock, observer: observer}, nil
}

// Run polls for eligible work and dispatches it until ctx is cancelled. It
// always returns ctx.Err() once every in-flight Dispatch call has finished
// (or hit its ShutdownGrace deadline after ctx was cancelled) — Run never
// returns while a Dispatch call it started is still running. Run is not
// safe to call concurrently on the same Worker; a crashed or exited Run may
// always be restarted by calling Run again, including from a freshly
// constructed Worker.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.cfg.Concurrency)
	b := newBackoff(w.cfg.MinBackoff, w.cfg.MaxBackoff, rand.New(rand.NewSource(w.clock.Now().UnixNano())))

	var inFlightMu sync.Mutex
	inFlight := make(map[int64]struct{})

	dispatchOne := func(invocationID int64) {
		defer wg.Done()
		defer func() {
			<-sem
			inFlightMu.Lock()
			delete(inFlight, invocationID)
			inFlightMu.Unlock()
		}()
		w.dispatchWithGrace(ctx, invocationID)
	}

pollLoop:
	for {
		select {
		case <-ctx.Done():
			break pollLoop
		default:
		}

		ids, err := w.work.ListEligible(ctx, w.cfg.PrincipalKey, w.cfg.BatchSize)
		if err != nil && ctx.Err() == nil {
			// A cancellation-induced error during shutdown is expected,
			// not an operational failure worth surfacing as one.
			w.observer.OnListError(err)
		}
		if err != nil || len(ids) == 0 {
			if !w.clock.Sleep(ctx, b.Next()) {
				break pollLoop
			}
			continue
		}

		dispatchedAny := false
		for _, id := range ids {
			select {
			case <-ctx.Done():
				break pollLoop
			case sem <- struct{}{}:
			}
			// The ctx.Done() and sem<-struct{}{} cases above can both be
			// ready at once; select picks pseudo-randomly, so re-check
			// explicitly to honor "stop accepting new work" precisely.
			if ctx.Err() != nil {
				<-sem
				break pollLoop
			}
			// Check-and-set, one id at a time: skips an id a prior,
			// still-running dispatch already claimed (the underlying
			// Dispatch claim is safe to retry concurrently — it wins or
			// loses atomically server-side — but retrying from here just
			// wastes a round trip), and also catches a duplicate id
			// appearing twice within this same batch.
			inFlightMu.Lock()
			if _, busy := inFlight[id]; busy {
				inFlightMu.Unlock()
				<-sem
				continue
			}
			inFlight[id] = struct{}{}
			inFlightMu.Unlock()
			dispatchedAny = true
			wg.Add(1)
			go dispatchOne(id)
		}

		if dispatchedAny {
			b.Reset()
			continue
		}
		// Every id this poll returned was already in flight: real work
		// exists, it is just busy, which is not the "no work" condition
		// backoff exists for. Pause briefly without advancing b, so a
		// genuine no-work streak elsewhere keeps escalating undisturbed.
		if !w.clock.Sleep(ctx, w.cfg.MinBackoff) {
			break pollLoop
		}
	}

	wg.Wait()
	return ctx.Err()
}

// dispatchWithGrace calls Dispatch on a context detached from ctx's
// cancellation, so an already-started dispatch is never aborted just
// because the process is shutting down. A ShutdownGrace timer only starts
// counting once ctx is actually cancelled — a dispatch that runs to
// completion without ctx ever being cancelled has no artificial timeout.
func (w *Worker) dispatchWithGrace(ctx context.Context, invocationID int64) {
	dispatchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-finished:
			return
		case <-ctx.Done():
		}
		timer := time.NewTimer(w.cfg.ShutdownGrace)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancel()
		case <-finished:
		}
	}()

	if _, err := w.dispatcher.Dispatch(dispatchCtx, invocationID); err != nil {
		w.observer.OnDispatchError(invocationID, err)
	}
}
