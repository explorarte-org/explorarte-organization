package driver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type fakeResumer struct {
	mu      sync.Mutex
	calls   map[int64]int
	handler func(ctx context.Context, id int64) (executive.Run, error)
}

func newFakeResumer(h func(ctx context.Context, id int64) (executive.Run, error)) *fakeResumer {
	return &fakeResumer{calls: make(map[int64]int), handler: h}
}

func (f *fakeResumer) ResumeDurable(ctx context.Context, rootTaskID int64) (executive.Run, error) {
	f.mu.Lock()
	f.calls[rootTaskID]++
	f.mu.Unlock()
	if f.handler != nil {
		return f.handler(ctx, rootTaskID)
	}
	return executive.Run{RootTaskID: rootTaskID, State: executive.StateCEOPlanning}, nil
}

func (f *fakeResumer) CallCount(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

type fakeRootSource struct {
	roots []int64
}

func (s fakeRootSource) ListExecutableRoots(ctx context.Context, limit int) ([]int64, error) {
	if len(s.roots) > limit {
		return s.roots[:limit], nil
	}
	return s.roots, nil
}

func TestClassifyResult(t *testing.T) {
	tests := []struct {
		name string
		run  executive.Run
		err  error
		want ResultClassification
	}{
		{
			name: "active continuing run",
			run:  executive.Run{State: executive.StateCEOPlanning},
			err:  nil,
			want: ResultContinue,
		},
		{
			name: "completed run",
			run:  executive.Run{State: executive.StateCompleted},
			err:  nil,
			want: ResultTerminal,
		},
		{
			name: "failed run",
			run:  executive.Run{State: executive.StateFailed},
			err:  nil,
			want: ResultTerminal,
		},
		{
			name: "active lease barrier is busy",
			run:  executive.Run{},
			err:  executive.ErrActiveLeaseBarrier,
			want: ResultBusy,
		},
		{
			name: "indeterminate tool execution is blocked safety",
			run:  executive.Run{},
			err:  executive.ErrIndeterminateToolExecution,
			want: ResultBlockedSafety,
		},
		{
			name: "orphaned model result is blocked safety",
			run:  executive.Run{},
			err:  executive.ErrOrphanedModelResult,
			want: ResultBlockedSafety,
		},
		{
			name: "ambiguous model outcome is blocked safety",
			run:  executive.Run{},
			err:  executive.ErrModelOutcomeAmbiguous,
			want: ResultBlockedSafety,
		},
		{
			name: "run blocked is blocked human",
			run:  executive.Run{},
			err:  executive.ErrRunBlocked,
			want: ResultBlockedHuman,
		},
		{
			name: "completion inconclusive is blocked human",
			run:  executive.Run{},
			err:  executive.ErrCompletionInconclusive,
			want: ResultBlockedHuman,
		},
		{
			name: "lease lost is retry later",
			run:  executive.Run{},
			err:  executive.ErrLeaseLost,
			want: ResultRetryLater,
		},
		{
			name: "prior execution unresolved is retry later",
			run:  executive.Run{},
			err:  executive.ErrPriorExecutionUnresolved,
			want: ResultRetryLater,
		},
		{
			name: "dispatch assignment required is retry later",
			run:  executive.Run{},
			err:  executive.ErrDispatchAssignmentRequired,
			want: ResultRetryLater,
		},
		{
			name: "unknown error is infra failure",
			run:  executive.Run{},
			err:  errors.New("database disconnected"),
			want: ResultInfraFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyResult(tt.run, tt.err)
			if got != tt.want {
				t.Errorf("ClassifyResult() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemoryRootCoordinator_MutualExclusion(t *testing.T) {
	ctx := context.Background()
	coord := NewMemoryRootCoordinator()

	claim1, claimed1, err1 := coord.TryClaimRoot(ctx, "org1", 100)
	if err1 != nil || !claimed1 {
		t.Fatalf("claim 1 failed: claimed=%v, err=%v", claimed1, err1)
	}

	// Second concurrent claim for same root must fail
	_, claimed2, err2 := coord.TryClaimRoot(ctx, "org1", 100)
	if err2 != nil {
		t.Fatalf("claim 2 unexpected error: %v", err2)
	}
	if claimed2 {
		t.Fatalf("claim 2 expected false (busy), got true")
	}

	// Different root can be claimed concurrently
	claimOther, claimedOther, errOther := coord.TryClaimRoot(ctx, "org1", 200)
	if errOther != nil || !claimedOther {
		t.Fatalf("claim other root failed: claimed=%v, err=%v", claimedOther, errOther)
	}
	_ = claimOther.Release(ctx)

	// After release, root 100 can be claimed again
	_ = claim1.Release(ctx)
	claim3, claimed3, err3 := coord.TryClaimRoot(ctx, "org1", 100)
	if err3 != nil || !claimed3 {
		t.Fatalf("claim 3 after release failed: claimed=%v, err=%v", claimed3, err3)
	}
	_ = claim3.Release(ctx)
}

func TestCampaignDriver_ConfigValidation(t *testing.T) {
	resumer := newFakeResumer(nil)
	roots := fakeRootSource{roots: []int64{101}}
	coord := NewMemoryRootCoordinator()

	// Zero / negative / extreme values should be clamped to sane bounds
	cfg := Config{
		OrganizationID: "org1",
		PollInterval:   0, // must clamp to sane default
		ErrorBackoff:   -1,
		BatchSize:      0,
		MaxConcurrency: 0,
	}

	driver, err := NewCampaignDriver(resumer, roots, coord, cfg)
	if err != nil {
		t.Fatalf("NewCampaignDriver failed: %v", err)
	}

	if driver.cfg.PollInterval < 100*time.Millisecond {
		t.Errorf("PollInterval %v < 100ms", driver.cfg.PollInterval)
	}
	if driver.cfg.ErrorBackoff < 100*time.Millisecond {
		t.Errorf("ErrorBackoff %v < 100ms", driver.cfg.ErrorBackoff)
	}
	if driver.cfg.BatchSize <= 0 {
		t.Errorf("BatchSize %v <= 0", driver.cfg.BatchSize)
	}
	if driver.cfg.MaxConcurrency <= 0 {
		t.Errorf("MaxConcurrency %v <= 0", driver.cfg.MaxConcurrency)
	}
}

func TestCampaignDriver_RunOnce_Advancement(t *testing.T) {
	ctx := context.Background()
	resumer := newFakeResumer(nil)
	roots := fakeRootSource{roots: []int64{101}}
	coord := NewMemoryRootCoordinator()

	driver, err := NewCampaignDriver(resumer, roots, coord, DefaultConfig("org1"))
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	metrics, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	if metrics.RootsDiscovered != 1 {
		t.Errorf("RootsDiscovered = %d, want 1", metrics.RootsDiscovered)
	}
	if metrics.RootsClaimed != 1 {
		t.Errorf("RootsClaimed = %d, want 1", metrics.RootsClaimed)
	}
	if metrics.ResumeCalls != 1 {
		t.Errorf("ResumeCalls = %d, want 1", metrics.ResumeCalls)
	}
	if metrics.ResumeSuccess != 1 {
		t.Errorf("ResumeSuccess = %d, want 1", metrics.ResumeSuccess)
	}
	if resumer.CallCount(101) != 1 {
		t.Errorf("resumer call count = %d, want 1", resumer.CallCount(101))
	}
}

func TestCampaignDriver_RunOnce_MultiRootFairness(t *testing.T) {
	ctx := context.Background()
	resumer := newFakeResumer(nil)
	roots := fakeRootSource{roots: []int64{101, 102, 103}}
	coord := NewMemoryRootCoordinator()

	driver, err := NewCampaignDriver(resumer, roots, coord, DefaultConfig("org1"))
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	metrics, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	if metrics.RootsDiscovered != 3 {
		t.Errorf("RootsDiscovered = %d, want 3", metrics.RootsDiscovered)
	}
	if metrics.RootsClaimed != 3 {
		t.Errorf("RootsClaimed = %d, want 3", metrics.RootsClaimed)
	}
	for _, id := range []int64{101, 102, 103} {
		if resumer.CallCount(id) != 1 {
			t.Errorf("root %d call count = %d, want 1", id, resumer.CallCount(id))
		}
	}
}

func TestCampaignDriver_RunOnce_BusySkipped(t *testing.T) {
	ctx := context.Background()
	resumer := newFakeResumer(nil)
	roots := fakeRootSource{roots: []int64{101}}
	coord := NewMemoryRootCoordinator()

	// Pre-claim root 101 as if another replica is driving it
	claim, claimed, _ := coord.TryClaimRoot(ctx, "org1", 101)
	if !claimed {
		t.Fatal("preclaim failed")
	}
	defer func() { _ = claim.Release(ctx) }()

	driver, err := NewCampaignDriver(resumer, roots, coord, DefaultConfig("org1"))
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	metrics, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	if metrics.RootsDiscovered != 1 {
		t.Errorf("RootsDiscovered = %d, want 1", metrics.RootsDiscovered)
	}
	if metrics.ResumeBusy != 1 {
		t.Errorf("ResumeBusy = %d, want 1", metrics.ResumeBusy)
	}
	if metrics.RootsClaimed != 0 {
		t.Errorf("RootsClaimed = %d, want 0", metrics.RootsClaimed)
	}
	if resumer.CallCount(101) != 0 {
		t.Errorf("resumer was called on busy root: %d", resumer.CallCount(101))
	}
}

func TestCampaignDriver_WakeupTriggersImmediatePoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int64
	resumer := newFakeResumer(func(ctx context.Context, id int64) (executive.Run, error) {
		runs.Add(1)
		return executive.Run{RootTaskID: id, State: executive.StateCEOPlanning}, nil
	})
	roots := fakeRootSource{roots: []int64{101}}
	coord := NewMemoryRootCoordinator()

	cfg := DefaultConfig("org1")
	cfg.PollInterval = 10 * time.Second // Long interval to prove wakeup works

	driver, err := NewCampaignDriver(resumer, roots, coord, cfg)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	go func() {
		_ = driver.Run(ctx)
	}()

	// Give loop a moment to start and complete initial pass
	time.Sleep(20 * time.Millisecond)
	initial := runs.Load()

	// Signal wakeup
	driver.Wakeup()
	time.Sleep(50 * time.Millisecond)

	if runs.Load() <= initial {
		t.Errorf("Wakeup did not trigger immediate poll: runs=%d, initial=%d", runs.Load(), initial)
	}
}

func TestCampaignDriver_LostWakeupDiscoveredByPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int64
	resumer := newFakeResumer(func(ctx context.Context, id int64) (executive.Run, error) {
		runs.Add(1)
		return executive.Run{RootTaskID: id, State: executive.StateCEOPlanning}, nil
	})
	roots := fakeRootSource{roots: []int64{201}}
	coord := NewMemoryRootCoordinator()

	cfg := DefaultConfig("org1")
	cfg.PollInterval = 150 * time.Millisecond // Bounded poll interval

	driver, err := NewCampaignDriver(resumer, roots, coord, cfg)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	go func() {
		_ = driver.Run(ctx)
	}()

	// NO WAKEUP CALLS - simulate lost wakeup channel entirely
	// Sleep long enough for multiple ticker periods
	time.Sleep(450 * time.Millisecond)

	if runs.Load() < 2 {
		t.Errorf("Polling alone failed to discover root without wakeup: runs=%d", runs.Load())
	}
}

func TestCampaignDriver_DuplicateWakeupNoDuplicateAdvancement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var running atomic.Int32
	var maxConcurrent atomic.Int32
	var totalAdvancements atomic.Int64

	resumer := newFakeResumer(func(ctx context.Context, id int64) (executive.Run, error) {
		curr := running.Add(1)
		if curr > maxConcurrent.Load() {
			maxConcurrent.Store(curr)
		}
		time.Sleep(30 * time.Millisecond) // hold execution
		running.Add(-1)
		totalAdvancements.Add(1)
		return executive.Run{RootTaskID: id, State: executive.StateCEOPlanning}, nil
	})
	roots := fakeRootSource{roots: []int64{301}}
	coord := NewMemoryRootCoordinator()

	cfg := DefaultConfig("org1")
	cfg.PollInterval = 5 * time.Second

	driver, err := NewCampaignDriver(resumer, roots, coord, cfg)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	go func() {
		_ = driver.Run(ctx)
	}()

	// Send rapid burst of 10 duplicate wakeups
	for i := 0; i < 10; i++ {
		driver.Wakeup()
	}

	time.Sleep(100 * time.Millisecond)

	if maxConcurrent.Load() > 1 {
		t.Errorf("Duplicate wakeups caused concurrent execution: maxConcurrent=%d, want 1", maxConcurrent.Load())
	}
}

func TestCampaignDriver_RetryLaterBackoffRespected(t *testing.T) {
	ctx := context.Background()
	var callCount atomic.Int64

	resumer := newFakeResumer(func(ctx context.Context, id int64) (executive.Run, error) {
		callCount.Add(1)
		// Simulates dispatch_assignment_required which maps to ResultRetryLater
		return executive.Run{}, executive.ErrDispatchAssignmentRequired
	})
	roots := fakeRootSource{roots: []int64{401}}
	coord := NewMemoryRootCoordinator()

	cfg := DefaultConfig("org1")
	cfg.ErrorBackoff = 200 * time.Millisecond // Bounded backoff for test

	driver, err := NewCampaignDriver(resumer, roots, coord, cfg)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	// First pass should discover, claim, attempt Resume, and encounter RetryLater
	m1, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 1 failed: %v", err)
	}
	if m1.ResumeRetryLater != 1 {
		t.Fatalf("ResumeRetryLater = %d, want 1", m1.ResumeRetryLater)
	}
	if callCount.Load() != 1 {
		t.Fatalf("callCount = %d, want 1", callCount.Load())
	}

	// Immediate second pass must SKIP root 401 because it is under backoff!
	m2, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 2 failed: %v", err)
	}
	// Resume calls must not have incremented
	if callCount.Load() != 1 {
		t.Errorf("immediate second pass executed root under backoff! callCount = %d, want 1", callCount.Load())
	}
	_ = m2

	// Wait for backoff duration to elapse
	time.Sleep(250 * time.Millisecond)

	// Third pass should now re-attempt the root
	m3, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 3 failed: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("third pass after backoff expiry did not re-attempt root: callCount = %d, want 2", callCount.Load())
	}
	_ = m3
}

func TestCampaignDriver_GracefulShutdown(t *testing.T) {
	t.Run("idle driver exits immediately on cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		resumer := newFakeResumer(nil)
		roots := fakeRootSource{roots: nil}
		coord := NewMemoryRootCoordinator()

		driver, _ := NewCampaignDriver(resumer, roots, coord, DefaultConfig("org1"))
		done := make(chan struct{})
		go func() {
			_ = driver.Run(ctx)
			close(done)
		}()

		cancel()
		select {
		case <-done:
			// Passed
		case <-time.After(500 * time.Millisecond):
			t.Fatal("driver did not shut down promptly on cancel")
		}
	})

	t.Run("in-flight resume respects cancel and cleans up", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		resumeBlocked := make(chan struct{})
		resumeDone := make(chan struct{})

		resumer := newFakeResumer(func(runCtx context.Context, id int64) (executive.Run, error) {
			close(resumeBlocked)
			<-runCtx.Done() // Block until context canceled
			close(resumeDone)
			return executive.Run{}, runCtx.Err()
		})
		roots := fakeRootSource{roots: []int64{501}}
		coord := NewMemoryRootCoordinator()

		driver, _ := NewCampaignDriver(resumer, roots, coord, DefaultConfig("org1"))
		done := make(chan struct{})
		go func() {
			_ = driver.Run(ctx)
			close(done)
		}()

		// Wait until resumer is blocked in-flight
		<-resumeBlocked
		cancel()

		select {
		case <-resumeDone:
			// resumer terminated on ctx.Done
		case <-time.After(1 * time.Second):
			t.Fatal("in-flight resumer did not exit on context cancel")
		}

		select {
		case <-done:
			// driver exited cleanly
		case <-time.After(1 * time.Second):
			t.Fatal("driver did not complete shutdown after in-flight cancel")
		}
	})
}

// TestCampaignDriver_PanicAdvancingOneRootDoesNotCrashProcessOrSiblingRoots
// proves the recover() added to the per-root goroutine actually does its
// job: a panic inside ResumeDurable for one root must not take down the
// whole worker process (this test itself completing at all is half the
// proof -- an unrecovered panic in a non-test goroutine crashes the test
// binary), must still release that root's claim (so it is claimable again
// on the very next sweep, matching every other error path's backoff/release
// behavior), and must never stop a concurrently-advancing sibling root from
// completing normally.
func TestCampaignDriver_PanicAdvancingOneRootDoesNotCrashProcessOrSiblingRoots(t *testing.T) {
	ctx := context.Background()
	const panicking, healthy = int64(601), int64(602)

	resumer := newFakeResumer(func(ctx context.Context, id int64) (executive.Run, error) {
		if id == panicking {
			panic("simulated ResumeDurable panic")
		}
		return executive.Run{RootTaskID: id, State: executive.StateCEOPlanning}, nil
	})
	roots := fakeRootSource{roots: []int64{panicking, healthy}}
	coord := NewMemoryRootCoordinator()

	var mu sync.Mutex
	var observed []struct {
		id             int64
		classification ResultClassification
		err            error
	}
	cfg := DefaultConfig("org1")
	cfg.MaxConcurrency = 2 // both roots must be able to run concurrently
	driver, err := NewCampaignDriver(resumer, roots, coord, cfg, WithObserver(func(id int64, classification ResultClassification, err error) {
		mu.Lock()
		observed = append(observed, struct {
			id             int64
			classification ResultClassification
			err            error
		}{id, classification, err})
		mu.Unlock()
	}))
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}

	metrics, err := driver.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce returned an error (the panic must be recovered, not propagated): %v", err)
	}

	// The healthy sibling root must have completed normally despite the
	// other goroutine panicking concurrently.
	if metrics.ResumeSuccess != 1 {
		t.Errorf("ResumeSuccess = %d, want 1 (the healthy sibling root)", metrics.ResumeSuccess)
	}
	if metrics.ResumeErrors != 1 {
		t.Errorf("ResumeErrors = %d, want 1 (the recovered panic)", metrics.ResumeErrors)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawPanic, sawHealthy bool
	for _, o := range observed {
		if o.id == panicking {
			sawPanic = true
			if o.classification != ResultInfraFailure {
				t.Errorf("panicking root classification = %v, want ResultInfraFailure", o.classification)
			}
			if o.err == nil {
				t.Error("panicking root observed with a nil error")
			}
		}
		if o.id == healthy && o.classification == ResultContinue {
			sawHealthy = true
		}
	}
	if !sawPanic {
		t.Fatal("observer never saw the panicking root")
	}
	if !sawHealthy {
		t.Fatal("observer never saw the healthy sibling root complete normally")
	}

	// The panicking root's claim must have been released (defer ordering:
	// the recover-defer runs after claim.Release's defer), so it is
	// immediately claimable again -- exactly like any other error path.
	claim, claimed, err := coord.TryClaimRoot(ctx, "org1", panicking)
	if err != nil || !claimed {
		t.Fatalf("root %d claim not released after recovered panic: claimed=%v, err=%v", panicking, claimed, err)
	}
	_ = claim.Release(ctx)
}
