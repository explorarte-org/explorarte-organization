package cellworker

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func testConfig() Config {
	return Config{
		PrincipalKey:  "principal-under-test",
		BatchSize:     10,
		Concurrency:   2,
		MinBackoff:    time.Millisecond,
		MaxBackoff:    4 * time.Millisecond,
		ShutdownGrace: 2 * time.Second,
	}
}

func TestNewRejectsInvalidConfigAndNilPorts(t *testing.T) {
	ws, d := &fakeWorkSource{}, &fakeDispatcher{}
	if _, err := New(Config{}, ws, d, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for zero-value config, got %v", err)
	}
	if _, err := New(testConfig(), nil, d, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for nil work source, got %v", err)
	}
	if _, err := New(testConfig(), ws, nil, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for nil dispatcher, got %v", err)
	}
}

func TestWorkerDispatchesEligibleInvocations(t *testing.T) {
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{1, 2, 3}}}}
	d := &fakeDispatcher{}
	w, err := New(testConfig(), ws, d, &fakeClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return len(d.dispatchedIDs()) >= 3 })
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	got := d.dispatchedIDs()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	got = dedupe(got)
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("expected invocations 1,2,3 dispatched at least once, got %v", got)
	}
}

func dedupe(ids []int64) []int64 {
	out := ids[:0]
	var last int64
	for i, id := range ids {
		if i == 0 || id != last {
			out = append(out, id)
		}
		last = id
	}
	return out
}

func TestWorkerRespectsConcurrencyLimit(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDispatcher{gate: gate}
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{1, 2, 3, 4, 5}}}}
	cfg := testConfig()
	cfg.Concurrency = 2
	w, err := New(cfg, ws, d, &fakeClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&d.inFlight) == 2 })
	// Give any (buggy) over-admission a chance to show up before we unblock.
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&d.inFlight); got != 2 {
		t.Fatalf("expected exactly 2 in flight at the concurrency cap, got %d", got)
	}

	close(gate)
	cancel()
	<-done

	if max := atomic.LoadInt32(&d.maxInFlight); max > 2 {
		t.Fatalf("concurrency limit violated: max in flight was %d, cap was 2", max)
	}
}

func TestWorkerBacksOffOnEmptyWork(t *testing.T) {
	ws := &fakeWorkSource{}
	d := &fakeDispatcher{}
	clock := &fakeClock{}
	w, err := New(testConfig(), ws, d, clock, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return clock.sleepCount() >= 3 && ws.callCount() >= 3 })
	cancel()
	<-done

	if len(d.dispatchedIDs()) != 0 {
		t.Fatalf("expected no dispatches when no work is eligible, got %v", d.dispatchedIDs())
	}
}

func TestWorkerBacksOffOnListError(t *testing.T) {
	ws := &fakeWorkSource{pages: []workPage{{err: errors.New("transient list failure")}}}
	d := &fakeDispatcher{}
	clock := &fakeClock{}
	observer := newFakeObserver()
	w, err := New(testConfig(), ws, d, clock, observer)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return clock.sleepCount() >= 3 })
	cancel()
	<-done

	if len(d.dispatchedIDs()) != 0 {
		t.Fatalf("expected no dispatches when ListEligible errors, got %v", d.dispatchedIDs())
	}
	if observer.listErrorCount() == 0 {
		t.Fatal("expected ListEligible's error to reach the Observer, got none")
	}
}

func TestWorkerDispatchErrorReachesObserver(t *testing.T) {
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{7}}}}
	d := &fakeDispatcher{err: errors.New("provider rejected the request")}
	observer := newFakeObserver()
	w, err := New(testConfig(), ws, d, &fakeClock{}, observer)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return observer.dispatchErrorCount(7) > 0 })
	cancel()
	<-done
}

func TestWorkerGracefulShutdownDrainsInFlight(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDispatcher{gate: gate}
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{42}}}}
	w, err := New(testConfig(), ws, d, &fakeClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&d.inFlight) == 1 })
	cancel()

	select {
	case <-done:
		t.Fatal("Run returned before the in-flight dispatch it started was allowed to finish")
	case <-time.After(50 * time.Millisecond):
	}

	close(gate)

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled after drain, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after the in-flight dispatch was unblocked")
	}

	if got := d.dispatchedIDs(); len(got) != 1 || got[0] != 42 {
		t.Fatalf("expected invocation 42 to complete despite shutdown, got %v", got)
	}
}

// TestWorkerLongDispatchNotCancelledWithoutShutdown proves ShutdownGrace is
// not a universal per-dispatch timeout: a dispatch that runs longer than
// ShutdownGrace, with Run's context never cancelled, must still complete
// successfully instead of being aborted mid-flight.
func TestWorkerLongDispatchNotCancelledWithoutShutdown(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDispatcher{gate: gate}
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{99}}}}
	cfg := testConfig()
	cfg.ShutdownGrace = 30 * time.Millisecond
	w, err := New(cfg, ws, d, &fakeClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&d.inFlight) == 1 })
	// Outlast ShutdownGrace several times over, with no shutdown requested.
	time.Sleep(6 * cfg.ShutdownGrace)
	close(gate)

	waitFor(t, time.Second, func() bool { return len(d.dispatchedIDs()) == 1 })
	cancel()
	<-done

	if got := d.dispatchedIDs(); len(got) != 1 || got[0] != 99 {
		t.Fatalf("expected invocation 99 to complete despite outlasting ShutdownGrace, got %v", got)
	}
}

// TestWorkerDispatchCancelledAfterShutdownGrace proves the grace period does
// apply, but only starting from when Run's context is actually cancelled: a
// dispatch that is still running ShutdownGrace after that point gets its
// context cancelled, and Run returns once that grace elapses rather than
// waiting for the dispatch indefinitely.
func TestWorkerDispatchCancelledAfterShutdownGrace(t *testing.T) {
	gate := make(chan struct{}) // never closed
	d := &fakeDispatcher{gate: gate}
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{5}}}}
	cfg := testConfig()
	cfg.ShutdownGrace = 50 * time.Millisecond
	observer := newFakeObserver()
	w, err := New(cfg, ws, d, &fakeClock{}, observer)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&d.inFlight) == 1 })
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if elapsed < cfg.ShutdownGrace {
			t.Fatalf("Run returned after %s, before ShutdownGrace (%s) elapsed", elapsed, cfg.ShutdownGrace)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within a generous multiple of ShutdownGrace")
	}

	waitFor(t, time.Second, func() bool { return observer.dispatchErrorCount(5) > 0 })
}

func TestWorkerRecoveryAfterRestartIsStateless(t *testing.T) {
	// Separate ports per simulated process so each run's own dispatches are
	// unambiguous, instead of sharing state that could let the first Run
	// silently finish both invocations before the second Run ever starts.
	runs := []struct {
		ws *fakeWorkSource
		d  *fakeDispatcher
		id int64
	}{
		{ws: &fakeWorkSource{pages: []workPage{{ids: []int64{1}}}}, d: &fakeDispatcher{}, id: 1},
		{ws: &fakeWorkSource{pages: []workPage{{ids: []int64{2}}}}, d: &fakeDispatcher{}, id: 2},
	}

	for _, run := range runs {
		w, err := New(testConfig(), run.ws, run.d, &fakeClock{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		waitFor(t, time.Second, func() bool { return len(run.d.dispatchedIDs()) >= 1 })
		cancel()
		<-done

		got := run.d.dispatchedIDs()
		if len(got) == 0 || got[0] != run.id {
			t.Fatalf("expected this run to dispatch invocation %d, got %v", run.id, got)
		}
	}
}
