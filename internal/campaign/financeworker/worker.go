// Package financeworker is the autonomous consumer of
// campaign.financial_review Task Engine tasks: the missing link between
// campaign.request_financial_review (which only ever creates a task and a
// durable request, deliberately never blocking the CEO chat turn on model
// completion) and FinanceService.ExecuteReviewTask (which was fully built,
// crash-safe and race-safe, but had zero production callers).
//
// This is infrastructure only, not business logic, deliberately mirroring
// internal/executive/driver's own shape (discover -> claim -> execute ->
// classify -> backoff) for the same reason that package does: the actual
// claim/execute/persist authority lives entirely in
// campaign.FinanceService.ExecuteReviewTask, which already performs its
// own Task Engine ClaimTaskByID/StartAttempt. Task Engine's row-level
// `FOR UPDATE SKIP LOCKED` claim is therefore already the full concurrency
// authority for one finance task -- unlike the Executive driver, which
// needs a PostgreSQL advisory lock because one Executive root spans many
// Resume calls across an indefinite lifetime, a finance review is exactly
// one bounded claim-execute-finalize cycle, so no advisory lock is added
// here (Task Engine leasing is not demonstrably insufficient for this
// shape).
package financeworker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// TaskSource discovers campaign.financial_review tasks ready to claim.
type TaskSource interface {
	ListReadyFinanceTasks(ctx context.Context, limit int) ([]int64, error)
}

// ReviewRequestResolver resolves a finance task ID to the review request it
// belongs to -- the canonical task-ID-to-request lookup, never inferred
// from task title/instructions text.
type ReviewRequestResolver interface {
	GetReviewRequestByTaskID(ctx context.Context, organizationID string, taskID int64) (campaign.CampaignFinancialReviewRequest, error)
}

// Executor is the one business-logic seam this package calls --
// *campaign.FinanceService itself in production. Abstracted so tests can
// substitute a narrower fake without constructing a full FinanceService.
type Executor interface {
	ExecuteReviewTask(ctx context.Context, params campaign.ExecuteReviewParams) (campaign.CampaignFinancialReview, bool, error)
}

// Config configures the autonomous finance worker.
type Config struct {
	OrganizationID    string
	WorkerID          string
	HolderPrincipalID string
	PollInterval      time.Duration
	ErrorBackoff      time.Duration
	BatchSize         int
}

func DefaultConfig(orgID string) Config {
	return Config{
		OrganizationID: orgID,
		PollInterval:   2 * time.Second,
		ErrorBackoff:   3 * time.Second,
		BatchSize:      16,
	}
}

// Metrics captures cumulative worker activity counters.
type Metrics struct {
	TasksDiscovered   int64
	ExecutionAttempts int64
	ExecutionSuccess  int64
	ExecutionBusy     int64
	ExecutionErrors   int64
	// RequestsFailed counts review requests moved to failed because their task ended without a review.
	RequestsFailed int64
}

// ResultClassification mirrors internal/executive/driver's own
// classification discipline: every outcome gets a name, never silent.
type ResultClassification string

const (
	ResultSuccess ResultClassification = "success"
	// ResultBusy means Task Engine's own claim lost a race (another
	// worker instance, or another replica, claimed this task first) or
	// the task is no longer ready -- expected under concurrency, never
	// logged as a failure or backed off.
	ResultBusy         ResultClassification = "busy"
	ResultInfraFailure ResultClassification = "infra_failure"
)

// Option configures optional Worker collaborators.
type Option func(*Worker)

// RequestReconciler moves out of pending every review request whose task ended without a review.
// The task is the authority on how its attempt ended; see
// campaignpostgres.Store.FailReviewRequestsOfTerminalTasks (audit 2026-09-26, finding 6).
type RequestReconciler interface {
	FailReviewRequestsOfTerminalTasks(ctx context.Context, organizationID string, limit int) ([]int64, error)
}

// WithRequestReconciler reconciles review requests at the start of every sweep.
func WithRequestReconciler(reconciler RequestReconciler) Option {
	return func(w *Worker) { w.requestReconciler = reconciler }
}

// WithObserver attaches an outcome observer for monitoring/testing.
func WithObserver(observe func(taskID int64, classification ResultClassification, err error)) Option {
	return func(w *Worker) { w.observe = observe }
}

// Worker autonomously discovers and executes campaign.financial_review
// tasks. It never approves, promotes, or calls Executive.Submit -- it only
// ever calls Executor.ExecuteReviewTask, which only ever writes the review.
type Worker struct {
	tasks          TaskSource
	reviewRequests ReviewRequestResolver
	executor       Executor
	cfg            Config
	wakeupCh       chan struct{}
	observe        func(taskID int64, classification ResultClassification, err error)

	requestReconciler RequestReconciler

	mu       sync.Mutex
	backoffs map[int64]time.Time

	tasksDiscovered   atomic.Int64
	executionAttempts atomic.Int64
	executionSuccess  atomic.Int64
	executionBusy     atomic.Int64
	executionErrors   atomic.Int64
	requestsFailed    atomic.Int64
}

// NewWorker validates and constructs a finance worker.
func NewWorker(taskSource TaskSource, reviewRequests ReviewRequestResolver, executor Executor, cfg Config, opts ...Option) (*Worker, error) {
	if taskSource == nil {
		return nil, errors.New("finance worker requires a task source")
	}
	if reviewRequests == nil {
		return nil, errors.New("finance worker requires a review request resolver")
	}
	if executor == nil {
		return nil, errors.New("finance worker requires an executor")
	}
	if cfg.PollInterval < 100*time.Millisecond {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.PollInterval > 10*time.Minute {
		cfg.PollInterval = 10 * time.Minute
	}
	if cfg.ErrorBackoff < 100*time.Millisecond {
		cfg.ErrorBackoff = 3 * time.Second
	}
	if cfg.BatchSize <= 0 || cfg.BatchSize > 128 {
		cfg.BatchSize = 16
	}
	w := &Worker{
		tasks: taskSource, reviewRequests: reviewRequests, executor: executor,
		cfg: cfg, wakeupCh: make(chan struct{}, 1), backoffs: make(map[int64]time.Time),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// Wakeup non-blockingly signals the worker to poll immediately.
func (w *Worker) Wakeup() {
	select {
	case w.wakeupCh <- struct{}{}:
	default:
	}
}

func (w *Worker) SnapshotMetrics() Metrics {
	return Metrics{
		TasksDiscovered:   w.tasksDiscovered.Load(),
		ExecutionAttempts: w.executionAttempts.Load(),
		ExecutionSuccess:  w.executionSuccess.Load(),
		ExecutionBusy:     w.executionBusy.Load(),
		ExecutionErrors:   w.executionErrors.Load(),
		RequestsFailed:    w.requestsFailed.Load(),
	}
}

// RunOnce performs one discovery and execution sweep. Tasks are processed
// sequentially, on purpose: a financial review is one bounded, MaxTurns=1
// model call, cheap enough that per-process concurrency adds risk (harder
// crash-window reasoning) without a demonstrated throughput need: multi-
// replica scaling already exists as the real lever, exactly like it does
// for the Executive driver.
func (w *Worker) RunOnce(ctx context.Context) (Metrics, error) {
	// Reconciling is its own concern: a failure to reconcile is reported and never stops the
	// sweep from executing the reviews that are ready.
	if w.requestReconciler != nil {
		failed, reconcileErr := w.requestReconciler.FailReviewRequestsOfTerminalTasks(ctx, w.cfg.OrganizationID, w.cfg.BatchSize)
		w.requestsFailed.Add(int64(len(failed)))
		if reconcileErr != nil {
			w.report(0, ResultInfraFailure, fmt.Errorf("reconcile review requests of terminal tasks: %w", reconcileErr))
		}
	}

	taskIDs, err := w.tasks.ListReadyFinanceTasks(ctx, w.cfg.BatchSize)
	if err != nil {
		return w.SnapshotMetrics(), err
	}
	w.tasksDiscovered.Add(int64(len(taskIDs)))

	now := time.Now()
	w.mu.Lock()
	eligible := make([]int64, 0, len(taskIDs))
	for _, id := range taskIDs {
		if until, blocked := w.backoffs[id]; blocked && now.Before(until) {
			continue
		}
		eligible = append(eligible, id)
	}
	w.mu.Unlock()

	for _, taskID := range eligible {
		if ctx.Err() != nil {
			break
		}
		w.executeOne(ctx, taskID)
	}
	return w.SnapshotMetrics(), nil
}

func (w *Worker) executeOne(ctx context.Context, taskID int64) {
	req, err := w.reviewRequests.GetReviewRequestByTaskID(ctx, w.cfg.OrganizationID, taskID)
	if err != nil {
		w.executionErrors.Add(1)
		w.setBackoff(taskID, w.cfg.ErrorBackoff)
		w.report(taskID, ResultInfraFailure, err)
		return
	}

	w.executionAttempts.Add(1)
	_, _, err = w.executor.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: w.cfg.OrganizationID, TaskID: taskID, ReviewRequestID: req.ID,
		WorkerID: w.cfg.WorkerID, HolderPrincipalID: w.cfg.HolderPrincipalID,
	})
	if err == nil {
		w.executionSuccess.Add(1)
		w.clearBackoff(taskID)
		w.report(taskID, ResultSuccess, nil)
		return
	}
	if errors.Is(err, tasks.ErrNotFound) {
		// Another worker instance (this process or a different replica)
		// already claimed this task, or it is no longer ready. Expected
		// under concurrency -- Task Engine's own row-level claim is the
		// authority here, this is not a failure.
		w.executionBusy.Add(1)
		w.report(taskID, ResultBusy, nil)
		return
	}
	w.executionErrors.Add(1)
	w.setBackoff(taskID, w.cfg.ErrorBackoff)
	w.report(taskID, ResultInfraFailure, err)
}

func (w *Worker) setBackoff(taskID int64, d time.Duration) {
	w.mu.Lock()
	w.backoffs[taskID] = time.Now().Add(d)
	w.mu.Unlock()
}

func (w *Worker) clearBackoff(taskID int64) {
	w.mu.Lock()
	delete(w.backoffs, taskID)
	w.mu.Unlock()
}

func (w *Worker) report(taskID int64, classification ResultClassification, err error) {
	if w.observe != nil {
		w.observe(taskID, classification, err)
	}
}

// Run executes the continuous background polling loop with advisory
// wakeup support -- wakeups are a pure optimization (see
// TestFinanceWorker_LostWakeupDiscoveredByPolling /
// ...DuplicateWakeupNoDuplicateExecution): dropping every wakeup still
// gets a task discovered by the next poll tick, and a burst of duplicate
// wakeups never runs the same task concurrently within this worker.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if _, err := w.RunOnce(ctx); err != nil && w.observe != nil {
			w.observe(0, ResultInfraFailure, err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-w.wakeupCh:
		}
	}
}
