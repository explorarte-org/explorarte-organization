package financeworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type fakeTaskSource struct {
	ids []int64
}

func (f fakeTaskSource) ListReadyFinanceTasks(ctx context.Context, limit int) ([]int64, error) {
	if len(f.ids) > limit {
		return f.ids[:limit], nil
	}
	return f.ids, nil
}

type fakeReviewRequests struct {
	byTaskID map[int64]campaign.CampaignFinancialReviewRequest
}

func (f fakeReviewRequests) GetReviewRequestByTaskID(ctx context.Context, organizationID string, taskID int64) (campaign.CampaignFinancialReviewRequest, error) {
	req, ok := f.byTaskID[taskID]
	if !ok {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return req, nil
}

type fakeExecutor struct {
	mu      sync.Mutex
	calls   map[int64]int
	handler func(taskID int64) error
}

func newFakeExecutor(handler func(taskID int64) error) *fakeExecutor {
	return &fakeExecutor{calls: make(map[int64]int), handler: handler}
}

func (f *fakeExecutor) ExecuteReviewTask(ctx context.Context, params campaign.ExecuteReviewParams) (campaign.CampaignFinancialReview, bool, error) {
	f.mu.Lock()
	f.calls[params.TaskID]++
	f.mu.Unlock()
	var err error
	if f.handler != nil {
		err = f.handler(params.TaskID)
	}
	if err != nil {
		return campaign.CampaignFinancialReview{}, false, err
	}
	return campaign.CampaignFinancialReview{ID: params.TaskID}, false, nil
}

func (f *fakeExecutor) callCount(taskID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[taskID]
}

func newTestWorker(t *testing.T, ids []int64, reqs map[int64]campaign.CampaignFinancialReviewRequest, exec Executor, cfg Config, opts ...Option) *Worker {
	t.Helper()
	w, err := NewWorker(fakeTaskSource{ids: ids}, fakeReviewRequests{byTaskID: reqs}, exec, cfg, opts...)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return w
}

func reqFor(taskID int64) map[int64]campaign.CampaignFinancialReviewRequest {
	return map[int64]campaign.CampaignFinancialReviewRequest{
		taskID: {ID: taskID, ReviewTaskID: taskID},
	}
}

func TestFinanceWorker_ConfigValidation(t *testing.T) {
	cfg := Config{PollInterval: 0, ErrorBackoff: -1, BatchSize: 0}
	w := newTestWorker(t, nil, nil, newFakeExecutor(nil), cfg)
	if w.cfg.PollInterval < 100*time.Millisecond {
		t.Errorf("PollInterval %v < 100ms", w.cfg.PollInterval)
	}
	if w.cfg.ErrorBackoff < 100*time.Millisecond {
		t.Errorf("ErrorBackoff %v < 100ms", w.cfg.ErrorBackoff)
	}
	if w.cfg.BatchSize <= 0 {
		t.Errorf("BatchSize %v <= 0", w.cfg.BatchSize)
	}
}

func TestFinanceWorker_RunOnce_ExecutesDiscoveredTask(t *testing.T) {
	exec := newFakeExecutor(nil)
	w := newTestWorker(t, []int64{101}, reqFor(101), exec, DefaultConfig("org1"))
	metrics, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if metrics.TasksDiscovered != 1 || metrics.ExecutionAttempts != 1 || metrics.ExecutionSuccess != 1 {
		t.Errorf("metrics = %+v, want discovered=1 attempts=1 success=1", metrics)
	}
	if exec.callCount(101) != 1 {
		t.Errorf("executor called %d times, want 1", exec.callCount(101))
	}
}

func TestFinanceWorker_BusyClassificationIsNotAFailure(t *testing.T) {
	exec := newFakeExecutor(func(int64) error { return wrappedNotFound{} })

	var observed []ResultClassification
	w := newTestWorker(t, []int64{101}, reqFor(101), exec, DefaultConfig("org1"),
		WithObserver(func(taskID int64, classification ResultClassification, err error) {
			observed = append(observed, classification)
		}))

	metrics, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if metrics.ExecutionBusy != 1 || metrics.ExecutionErrors != 0 {
		t.Errorf("metrics = %+v, want busy=1 errors=0", metrics)
	}
	if len(observed) != 1 || observed[0] != ResultBusy {
		t.Errorf("observed classifications = %v, want [busy]", observed)
	}

	// A busy task must NOT be backed off -- it should be retried on the
	// very next RunOnce, since the busy-ness (another worker's claim) is
	// not this worker's own failure to recover from.
	if _, blocked := w.backoffs[101]; blocked {
		t.Error("busy task was backed off, want no backoff")
	}
}

type wrappedNotFound struct{}

func (wrappedNotFound) Error() string { return "claim finance review task: task not found" }
func (wrappedNotFound) Unwrap() error { return tasks.ErrNotFound }

func TestFinanceWorker_InfraFailureIsBackedOff(t *testing.T) {
	failing := errors.New("simulated harness failure")
	exec := newFakeExecutor(func(int64) error { return failing })
	cfg := DefaultConfig("org1")
	cfg.ErrorBackoff = 200 * time.Millisecond

	w := newTestWorker(t, []int64{101}, reqFor(101), exec, cfg)

	m1, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce 1: %v", err)
	}
	if m1.ExecutionErrors != 1 {
		t.Fatalf("ExecutionErrors = %d, want 1", m1.ExecutionErrors)
	}
	if exec.callCount(101) != 1 {
		t.Fatalf("callCount after RunOnce 1 = %d, want 1", exec.callCount(101))
	}

	// Immediate second pass must SKIP the backed-off task.
	if _, err = w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce 2: %v", err)
	}
	if exec.callCount(101) != 1 {
		t.Errorf("callCount after immediate RunOnce 2 = %d, want still 1 (backed off)", exec.callCount(101))
	}

	time.Sleep(250 * time.Millisecond)
	if _, err = w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce 3: %v", err)
	}
	if exec.callCount(101) != 2 {
		t.Errorf("callCount after RunOnce 3 (post-backoff) = %d, want 2", exec.callCount(101))
	}
}

func TestFinanceWorker_LostWakeupDiscoveredByPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int64
	exec := newFakeExecutor(func(int64) error { calls.Add(1); return nil })
	cfg := DefaultConfig("org1")
	cfg.PollInterval = 100 * time.Millisecond

	w := newTestWorker(t, []int64{201}, reqFor(201), exec, cfg)

	go func() { _ = w.Run(ctx) }()

	// NO wakeup calls -- simulate a lost wakeup channel entirely.
	time.Sleep(150 * time.Millisecond)

	if calls.Load() < 1 {
		t.Errorf("polling alone failed to discover and execute task: calls=%d", calls.Load())
	}
}

func TestFinanceWorker_DuplicateWakeupNoDuplicateExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var running atomic.Int32
	var maxConcurrent atomic.Int32
	exec := newFakeExecutor(func(int64) error {
		cur := running.Add(1)
		if cur > maxConcurrent.Load() {
			maxConcurrent.Store(cur)
		}
		time.Sleep(30 * time.Millisecond)
		running.Add(-1)
		return nil
	})
	cfg := DefaultConfig("org1")
	cfg.PollInterval = 5 * time.Second

	w := newTestWorker(t, []int64{301}, reqFor(301), exec, cfg)
	go func() { _ = w.Run(ctx) }()

	for i := 0; i < 10; i++ {
		w.Wakeup()
	}
	time.Sleep(100 * time.Millisecond)

	if maxConcurrent.Load() > 1 {
		t.Errorf("duplicate wakeups caused concurrent execution: maxConcurrent=%d, want 1", maxConcurrent.Load())
	}
}

func TestFinanceWorker_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := newTestWorker(t, nil, nil, newFakeExecutor(nil), DefaultConfig("org1"))

	done := make(chan struct{})
	go func() { _ = w.Run(ctx); close(done) }()
	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not shut down promptly on cancel")
	}
}
