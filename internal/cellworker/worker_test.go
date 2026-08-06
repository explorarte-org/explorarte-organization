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
	if _, err := New(Config{}, ws, d, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for zero-value config, got %v", err)
	}
	if _, err := New(testConfig(), nil, d, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for nil work source, got %v", err)
	}
	if _, err := New(testConfig(), ws, nil, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for nil dispatcher, got %v", err)
	}
}

func TestWorkerDispatchesEligibleInvocations(t *testing.T) {
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{1, 2, 3}}}}
	d := &fakeDispatcher{}
	w, err := New(testConfig(), ws, d, &fakeClock{})
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
	w, err := New(cfg, ws, d, &fakeClock{})
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
	w, err := New(testConfig(), ws, d, clock)
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
	w, err := New(testConfig(), ws, d, clock)
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
}

func TestWorkerGracefulShutdownDrainsInFlight(t *testing.T) {
	gate := make(chan struct{})
	d := &fakeDispatcher{gate: gate}
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{42}}}}
	w, err := New(testConfig(), ws, d, &fakeClock{})
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

func TestWorkerRecoveryAfterRestartIsStateless(t *testing.T) {
	ws := &fakeWorkSource{pages: []workPage{{ids: []int64{1}}, {ids: []int64{2}}}}
	d := &fakeDispatcher{}

	for i := 0; i < 2; i++ {
		w, err := New(testConfig(), ws, d, &fakeClock{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		waitFor(t, time.Second, func() bool { return ws.callCount() >= i+1 })
		cancel()
		<-done
	}

	got := d.dispatchedIDs()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	got = dedupe(got)
	if len(got) < 2 || got[0] != 1 || got[len(got)-1] != 2 {
		t.Fatalf("expected a freshly constructed Worker to keep making progress after a simulated restart, got %v", got)
	}
}
