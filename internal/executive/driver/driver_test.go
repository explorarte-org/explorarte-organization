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

	release1, claimed1, err1 := coord.TryClaimRoot(ctx, "org1", 100)
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
	releaseOther, claimedOther, errOther := coord.TryClaimRoot(ctx, "org1", 200)
	if errOther != nil || !claimedOther {
		t.Fatalf("claim other root failed: claimed=%v, err=%v", claimedOther, errOther)
	}
	releaseOther()

	// After release, root 100 can be claimed again
	release1()
	release3, claimed3, err3 := coord.TryClaimRoot(ctx, "org1", 100)
	if err3 != nil || !claimed3 {
		t.Fatalf("claim 3 after release failed: claimed=%v, err=%v", claimed3, err3)
	}
	release3()
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
	release, claimed, _ := coord.TryClaimRoot(ctx, "org1", 101)
	if !claimed {
		t.Fatal("preclaim failed")
	}
	defer release()

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
		return executive.Run{RootTaskID: id, State: executive.StateCompleted}, nil
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
