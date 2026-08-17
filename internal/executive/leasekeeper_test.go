package executive

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// manualTicker replaces wall-clock time so a heartbeat cadence measured in
// minutes can be exercised in microseconds, deterministically.
type manualTicker struct {
	c       chan time.Time
	stopped chan struct{}
}

func newManualTicker() *manualTicker {
	return &manualTicker{c: make(chan time.Time, 8), stopped: make(chan struct{})}
}

func (t *manualTicker) Ticks() <-chan time.Time { return t.c }
func (t *manualTicker) Stop() {
	select {
	case <-t.stopped:
	default:
		close(t.stopped)
	}
}
func (t *manualTicker) tick() { t.c <- time.Unix(0, 0) }

func keeperOrchestrator(t *testing.T, tasksPort *memoryTasks, ticker LeaseTicker) *Orchestrator {
	t.Helper()
	orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), &fakeCompletion{verdict: CompletionPass})
	WithLeaseKeeper(LeaseKeeperConfig{
		Interval: time.Millisecond, Extension: executiveLeaseTTL,
		NewTicker: func(time.Duration) LeaseTicker { return ticker },
	})(orchestrator)
	return orchestrator
}

func leasedFixture(t *testing.T, tasksPort *memoryTasks, principalID string) LeaseRecord {
	t.Helper()
	task, _, _ := tasksPort.CreateTask(context.Background(), CreateTaskCommand{
		RequestedByRoleID: CEORoleID, AssignedRoleID: "ingenieria_ia/orquestador", IdempotencyKey: "keeper-" + principalID,
		Title: "t", Instructions: "t", AcceptanceCriteria: []string{"x"}, CorrelationID: "executive:keeper",
	})
	_, _, lease, err := tasksPort.ClaimTask(context.Background(), ClaimTaskCommand{
		TaskID: task.ID, WorkerID: orchestratorWorkerID, HolderPrincipalID: principalID,
		AssignedRoleID: "ingenieria_ia/orquestador", LeaseDuration: executiveLeaseTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestLeaseKeeperHeartbeatsAsTheLeaseHolderAndShutsDownCleanly(t *testing.T) {
	tasksPort := newMemoryTasks()
	ticker := newManualTicker()
	orchestrator := keeperOrchestrator(t, tasksPort, ticker)
	lease := leasedFixture(t, tasksPort, "5150")

	before := runtime.NumGoroutine()
	execCtx, keeper := orchestrator.startLeaseKeeper(context.Background(), lease.TaskID, lease, "5150")
	ticker.tick()
	waitFor(t, func() bool { return keeper.heartbeats() >= 1 })

	if err := keeper.stop(); err != nil {
		t.Fatalf("clean stop reported failure: %v", err)
	}
	select {
	case <-keeper.exited:
	default:
		t.Fatal("stop returned before the keeper goroutine exited")
	}
	if execCtx.Err() == nil {
		t.Fatal("stop must release the execution context it created")
	}
	for _, actor := range tasksPort.heartbeatActorLog() {
		if actor != "5150" {
			t.Fatalf("heartbeat actor=%q want the canonical lease holder", actor)
		}
	}
	waitForGoroutines(t, before)
}

// TestLeaseKeeperFailureCancelsExecutionAndIsReported is the property the
// whole keeper exists for: a lost lease reaches into the synchronous run and
// stops it, and the reason survives to the caller.
func TestLeaseKeeperFailureCancelsExecutionAndIsReported(t *testing.T) {
	tasksPort := newMemoryTasks()
	ticker := newManualTicker()
	orchestrator := keeperOrchestrator(t, tasksPort, ticker)
	lease := leasedFixture(t, tasksPort, "5151")

	before := runtime.NumGoroutine()
	execCtx, keeper := orchestrator.startLeaseKeeper(context.Background(), lease.TaskID, lease, "5151")
	heartbeatFailure := errors.New("lease expired under us")
	tasksPort.failHeartbeats(heartbeatFailure)
	ticker.tick()

	select {
	case <-execCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a failed heartbeat must cancel the execution context")
	}
	if err := keeper.stop(); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("stop err=%v want %v", err, heartbeatFailure)
	}
	waitForGoroutines(t, before)
}

// TestLeaseKeeperStopIsIdempotentAndNeverOrphansItsGoroutine covers the two
// shutdown paths that are easy to get wrong: stopping twice, and a parent
// context that ends before stop is ever called.
func TestLeaseKeeperStopIsIdempotentAndNeverOrphansItsGoroutine(t *testing.T) {
	tasksPort := newMemoryTasks()
	orchestrator := keeperOrchestrator(t, tasksPort, newManualTicker())
	lease := leasedFixture(t, tasksPort, "5152")

	before := runtime.NumGoroutine()
	_, keeper := orchestrator.startLeaseKeeper(context.Background(), lease.TaskID, lease, "5152")
	if err := keeper.stop(); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := keeper.stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}

	parent, cancelParent := context.WithCancel(context.Background())
	_, second := orchestrator.startLeaseKeeper(parent, lease.TaskID, lease, "5152")
	cancelParent()
	if err := second.stop(); err != nil {
		t.Fatalf("stop after parent cancellation reported a lease failure: %v", err)
	}
	waitForGoroutines(t, before)
}

func TestLeaseKeeperCadenceStaysWellInsideTheLeaseTTL(t *testing.T) {
	defaults := DefaultLeaseKeeperConfig()
	if defaults.Extension != executiveLeaseTTL {
		t.Fatalf("extension=%v want the lease TTL %v", defaults.Extension, executiveLeaseTTL)
	}
	if defaults.Interval <= 0 || defaults.Interval > defaults.Extension/2 {
		t.Fatalf("interval=%v leaves no room for a slow heartbeat before %v", defaults.Interval, defaults.Extension)
	}
	// A caller that asks for a cadence as long as the lease itself gets a
	// usable one instead: a lease that is only renewed exactly when it expires
	// is a lease that expires.
	normalized := LeaseKeeperConfig{Interval: executiveLeaseTTL, Extension: executiveLeaseTTL}.normalized()
	if normalized.Interval > normalized.Extension/2 {
		t.Fatalf("normalized interval=%v extension=%v", normalized.Interval, normalized.Extension)
	}
	if normalized.NewTicker == nil {
		t.Fatal("normalized config must always have a time source")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not reached in time")
}

func waitForGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("keeper goroutines outlived the run: before=%d now=%d", before, runtime.NumGoroutine())
}
