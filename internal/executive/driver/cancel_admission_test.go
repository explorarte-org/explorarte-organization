package driver

// Audit 2026-09-26, finding 5: a sweep cancelled while waiting for a concurrency slot must not
// start a goroutine that never acquired one; RunOnce must return.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type slotWaitObservedContext struct {
	context.Context
	calls   atomic.Int32
	waiting chan struct{}
}

func (c *slotWaitObservedContext) Done() <-chan struct{} {
	if c.calls.Add(1) == 2 {
		close(c.waiting)
	}
	return c.Context.Done()
}
func TestACancelledSweepWaitingForASlotStartsNothingAndReturns(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &slotWaitObservedContext{Context: base, waiting: make(chan struct{})}
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	resumer := newFakeResumer(func(_ context.Context, id int64) (executive.Run, error) {
		if id == 1 {
			<-release
		} else {
			close(secondStarted)
		}
		return executive.Run{RootTaskID: id, State: executive.StateCEOPlanning}, nil
	})
	cfg := DefaultConfig("audit")
	cfg.MaxConcurrency = 1
	d, err := NewCampaignDriver(resumer, fakeRootSource{roots: []int64{1, 2}}, NewMemoryRootCoordinator(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { d.RunOnce(ctx); close(done) }()
	select {
	case <-ctx.waiting:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("did not reach second concurrency wait")
	}
	cancel()
	select {
	case <-secondStarted:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("RunOnce deadlocked after cancellation while waiting for semaphore; goroutine releases a token it never acquired")
	}
}
