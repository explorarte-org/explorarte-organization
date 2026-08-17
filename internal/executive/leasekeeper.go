package executive

import (
	"context"
	"sync"
	"time"
)

// A synchronous Harness run blocks for as long as the provider takes, and the
// invocation deadline (10 minutes by default) is deliberately longer than the
// task lease TTL (5 minutes). Something therefore has to keep the lease alive
// while the run is in flight, and that something is the Executive: the Harness
// owns cognitive trajectory, not task state, and putting heartbeats inside it
// would give it a second, hidden claim on the task engine.
//
// The keeper is a small isolated lifecycle with one rule that matters: if it
// could not keep the lease, the run's result is not recordable, whatever the
// Harness says about it. See leaseKeeper.stop.

// LeaseTicker is the keeper's only source of time. It exists so tests can
// drive heartbeats deterministically instead of sleeping through minutes of
// real lease TTL.
type LeaseTicker interface {
	Ticks() <-chan time.Time
	Stop()
}

type realLeaseTicker struct{ ticker *time.Ticker }

func (t realLeaseTicker) Ticks() <-chan time.Time { return t.ticker.C }
func (t realLeaseTicker) Stop()                   { t.ticker.Stop() }

// LeaseKeeperConfig configures heartbeat cadence. Interval must be well under
// Extension: a heartbeat that fires as often as the lease expires leaves no
// room for a single slow round trip.
type LeaseKeeperConfig struct {
	Interval  time.Duration
	Extension time.Duration
	NewTicker func(time.Duration) LeaseTicker
}

func DefaultLeaseKeeperConfig() LeaseKeeperConfig {
	return LeaseKeeperConfig{
		Interval:  executiveLeaseTTL / 3,
		Extension: executiveLeaseTTL,
		NewTicker: func(d time.Duration) LeaseTicker { return realLeaseTicker{ticker: time.NewTicker(d)} },
	}
}

func (c LeaseKeeperConfig) normalized() LeaseKeeperConfig {
	if c.Extension <= 0 {
		c.Extension = executiveLeaseTTL
	}
	if c.Interval <= 0 || c.Interval > c.Extension/2 {
		c.Interval = c.Extension / 3
	}
	if c.NewTicker == nil {
		c.NewTicker = func(d time.Duration) LeaseTicker { return realLeaseTicker{ticker: time.NewTicker(d)} }
	}
	return c
}

// WithLeaseKeeper overrides heartbeat cadence and time source.
func WithLeaseKeeper(config LeaseKeeperConfig) OrchestratorOption {
	return func(o *Orchestrator) { o.leaseKeeper = config.normalized() }
}

type leaseKeeper struct {
	cancel context.CancelFunc
	done   chan struct{}
	exited chan struct{}
	once   sync.Once

	mu       sync.Mutex
	lease    LeaseRecord
	failure  error
	beats    int
	observed bool
}

// startLeaseKeeper returns the context the Harness run must use. Cancelling it
// is how a lost lease reaches into a synchronous provider call that would
// otherwise keep running under authority it no longer has.
func (o *Orchestrator) startLeaseKeeper(ctx context.Context, taskID int64, lease LeaseRecord, actorID string) (context.Context, *leaseKeeper) {
	config := o.leaseKeeper.normalized()
	execCtx, cancel := context.WithCancel(ctx)
	keeper := &leaseKeeper{cancel: cancel, done: make(chan struct{}), exited: make(chan struct{}), lease: lease}
	go func() {
		defer close(keeper.exited)
		ticker := config.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-keeper.done:
				return
			case <-ctx.Done():
				// The caller's own context ended. The run is going away with
				// it; there is nothing left to keep alive and no failure to
				// attribute to the lease.
				return
			case <-ticker.Ticks():
				// Heartbeats deliberately use the PARENT context, not execCtx:
				// execCtx is the thing this goroutine cancels, and a heartbeat
				// that cancelled itself could never report why.
				updated, err := o.tasks.Heartbeat(ctx, keeper.currentLease(), actorID, config.Extension)
				if err != nil {
					keeper.fail(err)
					keeper.cancel()
					return
				}
				keeper.observe(updated)
				o.rememberLease(taskID, updated)
			}
		}
	}()
	return execCtx, keeper
}

// stop ends the keeper and waits for its goroutine to actually exit before
// returning. The wait is the point: the caller must not interpret a run
// outcome while a heartbeat is still in flight, because the heartbeat may be
// the thing that invalidates it.
func (k *leaseKeeper) stop() error {
	k.once.Do(func() { close(k.done) })
	<-k.exited
	k.cancel()
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.failure
}

func (k *leaseKeeper) currentLease() LeaseRecord {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.lease
}

func (k *leaseKeeper) heartbeats() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.beats
}

func (k *leaseKeeper) observe(lease LeaseRecord) {
	k.mu.Lock()
	defer k.mu.Unlock()
	// A heartbeat returns the durable lease row; the plaintext token is
	// process-local and never round-trips, so it is carried forward here.
	if lease.LeaseToken == "" {
		lease.LeaseToken = k.lease.LeaseToken
	}
	k.lease = lease
	k.beats++
	k.observed = true
}

func (k *leaseKeeper) fail(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failure == nil {
		k.failure = err
	}
}
