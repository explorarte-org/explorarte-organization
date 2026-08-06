package cellworker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// fakeWorkSource replays a fixed sequence of ListEligible results, one per
// call; once exhausted (or if never configured) it returns "no work, no
// error" forever.
type fakeWorkSource struct {
	mu    sync.Mutex
	calls int
	pages []workPage
}

type workPage struct {
	ids []int64
	err error
}

// ListEligible returns each configured page exactly once, in order, then
// (nil, nil) forever after — mirroring real eligibility, where a dispatched
// invocation stops being returned rather than being handed out repeatedly.
func (f *fakeWorkSource) ListEligible(_ context.Context, _ string, _ int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	idx := f.calls - 1
	if idx >= len(f.pages) {
		return nil, nil
	}
	page := f.pages[idx]
	return page.ids, page.err
}

func (f *fakeWorkSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// repeatingWorkSource always returns the same fixed ids, mirroring a
// WorkSource whose durable state has not yet reflected an in-flight
// dispatch attempt (e.g. status still 'requested'/'claimed').
type repeatingWorkSource struct {
	mu    sync.Mutex
	ids   []int64
	calls int
}

func (f *repeatingWorkSource) ListEligible(_ context.Context, _ string, _ int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := make([]int64, len(f.ids))
	copy(out, f.ids)
	return out, nil
}

func (f *repeatingWorkSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeDispatcher records every invocationID it is called with. If gate is
// non-nil, each call blocks on it before returning, which lets tests hold a
// dispatch "in flight" to exercise concurrency limits and graceful shutdown.
type fakeDispatcher struct {
	mu          sync.Mutex
	dispatched  []int64
	inFlight    int32
	maxInFlight int32
	gate        <-chan struct{}
	err         error
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, invocationID int64) (modelruntime.DispatchResult, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	for {
		prev := atomic.LoadInt32(&f.maxInFlight)
		if n <= prev || atomic.CompareAndSwapInt32(&f.maxInFlight, prev, n) {
			break
		}
	}
	defer atomic.AddInt32(&f.inFlight, -1)

	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			// A real Dispatcher must not report a cancelled call as a
			// completed dispatch: return promptly with ctx's error instead
			// of falling through to record success below.
			return modelruntime.DispatchResult{}, ctx.Err()
		}
	}

	f.mu.Lock()
	f.dispatched = append(f.dispatched, invocationID)
	f.mu.Unlock()

	return modelruntime.DispatchResult{Invocation: modelruntime.Invocation{ID: invocationID}}, f.err
}

func (f *fakeDispatcher) dispatchedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.dispatched))
	copy(out, f.dispatched)
	return out
}

// fakeClock makes Sleep return immediately (recording the requested
// duration) so backoff tests run fast and deterministically, while still
// honoring ctx cancellation the same way the real clock does.
type fakeClock struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (f *fakeClock) Now() time.Time { return time.Unix(0, 0) }

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) bool {
	f.mu.Lock()
	f.sleeps = append(f.sleeps, d)
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func (f *fakeClock) sleepCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sleeps)
}

// fakeObserver records every OnListError/OnDispatchError call so tests can
// assert that failures are surfaced rather than silently discarded.
type fakeObserver struct {
	mu             sync.Mutex
	listErrors     []error
	dispatchErrors map[int64][]error
}

func newFakeObserver() *fakeObserver {
	return &fakeObserver{dispatchErrors: make(map[int64][]error)}
}

func (f *fakeObserver) OnListError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErrors = append(f.listErrors, err)
}

func (f *fakeObserver) OnDispatchError(invocationID int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatchErrors[invocationID] = append(f.dispatchErrors[invocationID], err)
}

func (f *fakeObserver) listErrorCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.listErrors)
}

func (f *fakeObserver) dispatchErrorCount(invocationID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dispatchErrors[invocationID])
}
