package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// ExecutiveResumer abstracts Orchestrator.ResumeDurable.
type ExecutiveResumer interface {
	ResumeDurable(ctx context.Context, rootTaskID int64) (executive.Run, error)
}

// Config configures the autonomous campaign driver.
type Config struct {
	OrganizationID string
	PollInterval   time.Duration
	ErrorBackoff   time.Duration
	BatchSize      int
	MaxConcurrency int
}

func DefaultConfig(orgID string) Config {
	return Config{
		OrganizationID: orgID,
		PollInterval:   2 * time.Second,
		ErrorBackoff:   3 * time.Second,
		BatchSize:      16,
		MaxConcurrency: 4,
	}
}

// Metrics captures cumulative driver activity counters.
type Metrics struct {
	RootsDiscovered  int64
	RootsClaimed     int64
	ResumeCalls      int64
	ResumeSuccess    int64
	ResumeBusy       int64
	ResumeBlocked    int64
	ResumeTerminal   int64
	ResumeRetryLater int64
	ResumeErrors     int64
}

// Option configures optional CampaignDriver collaborators.
type Option func(*CampaignDriver)

// WithExecutionReconciler wires model execution reconciliation.
func WithExecutionReconciler(reconciler executive.ExecutionReconciler) Option {
	return func(d *CampaignDriver) {
		d.executions = reconciler
	}
}

// WithObserver attaches an outcome observer for monitoring/testing.
func WithObserver(observe func(rootID int64, classification ResultClassification, err error)) Option {
	return func(d *CampaignDriver) {
		d.observe = observe
	}
}

// CampaignDriver autonomously discovers and drives approved Executive campaign roots.
type CampaignDriver struct {
	resumer     ExecutiveResumer
	roots       executive.RootSource
	coordinator RootCoordinator
	executions  executive.ExecutionReconciler
	cfg         Config
	wakeupCh    chan struct{}
	observe     func(rootID int64, classification ResultClassification, err error)

	mu       sync.Mutex
	backoffs map[int64]time.Time

	rootsDiscovered  atomic.Int64
	rootsClaimed     atomic.Int64
	resumeCalls      atomic.Int64
	resumeSuccess    atomic.Int64
	resumeBusy       atomic.Int64
	resumeBlocked    atomic.Int64
	resumeTerminal   atomic.Int64
	resumeRetryLater atomic.Int64
	resumeErrors     atomic.Int64
}

// NewCampaignDriver instantiates a validated campaign driver.
func NewCampaignDriver(resumer ExecutiveResumer, roots executive.RootSource, coordinator RootCoordinator, cfg Config, options ...Option) (*CampaignDriver, error) {
	if resumer == nil {
		return nil, errors.New("campaign driver requires an executive resumer")
	}
	if roots == nil {
		return nil, errors.New("campaign driver requires a root source")
	}
	if coordinator == nil {
		return nil, errors.New("campaign driver requires a root coordinator")
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
	if cfg.MaxConcurrency <= 0 || cfg.MaxConcurrency > 32 {
		cfg.MaxConcurrency = 4
	}

	d := &CampaignDriver{
		resumer:     resumer,
		roots:       roots,
		coordinator: coordinator,
		cfg:         cfg,
		wakeupCh:    make(chan struct{}, 1),
		backoffs:    make(map[int64]time.Time),
	}
	for _, opt := range options {
		opt(d)
	}
	return d, nil
}

// Wakeup non-blockingly signals the driver to poll immediately.
func (d *CampaignDriver) Wakeup() {
	select {
	case d.wakeupCh <- struct{}{}:
	default:
	}
}

// SnapshotMetrics returns current cumulative counters.
func (d *CampaignDriver) SnapshotMetrics() Metrics {
	return Metrics{
		RootsDiscovered:  d.rootsDiscovered.Load(),
		RootsClaimed:     d.rootsClaimed.Load(),
		ResumeCalls:      d.resumeCalls.Load(),
		ResumeSuccess:    d.resumeSuccess.Load(),
		ResumeBusy:       d.resumeBusy.Load(),
		ResumeBlocked:    d.resumeBlocked.Load(),
		ResumeTerminal:   d.resumeTerminal.Load(),
		ResumeRetryLater: d.resumeRetryLater.Load(),
		ResumeErrors:     d.resumeErrors.Load(),
	}
}

// RunOnce performs one discovery and advancement sweep.
func (d *CampaignDriver) RunOnce(ctx context.Context) (Metrics, error) {
	if d.executions != nil {
		_ = d.executions.Reconcile(ctx, d.cfg.BatchSize)
	}

	rootIDs, err := d.roots.ListExecutableRoots(ctx, d.cfg.BatchSize)
	if err != nil {
		return d.SnapshotMetrics(), err
	}

	d.rootsDiscovered.Add(int64(len(rootIDs)))
	if len(rootIDs) == 0 {
		return d.SnapshotMetrics(), nil
	}

	// Filter out roots currently under retry backoff or suppression.
	now := time.Now()
	eligible := make([]int64, 0, len(rootIDs))
	d.mu.Lock()
	for _, id := range rootIDs {
		retryUntil, hasBackoff := d.backoffs[id]
		if hasBackoff && now.Before(retryUntil) {
			continue
		}
		eligible = append(eligible, id)
	}
	d.mu.Unlock()

	if len(eligible) == 0 {
		return d.SnapshotMetrics(), nil
	}

	// Bound concurrency and drive each root fairly.
	sem := make(chan struct{}, d.cfg.MaxConcurrency)
	var wg sync.WaitGroup

	for _, rootID := range eligible {
		if ctx.Err() != nil {
			break
		}

		select {
		case <-ctx.Done():
			break
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(id int64) {
			// Matches the recover-and-continue convention every other
			// background reconciler in this codebase already uses (see
			// App.reconcileTasksOnce/reconcileStagingOnce in
			// internal/app/app.go): a panic advancing one root must not take
			// the whole worker process down with it -- especially here,
			// where doing so would also abort every OTHER root concurrently
			// mid-Resume in sibling goroutines. Registered first so it runs
			// last (defers unwind LIFO): the semaphore/WaitGroup release and
			// claim.Release below still run normally on a panic, so the
			// pinned session is never left dangling.
			defer func() {
				if recovered := recover(); recovered != nil {
					d.resumeErrors.Add(1)
					d.mu.Lock()
					d.backoffs[id] = time.Now().Add(d.cfg.ErrorBackoff)
					d.mu.Unlock()
					if d.observe != nil {
						d.observe(id, ResultInfraFailure, fmt.Errorf("campaign driver: recovered panic advancing root %d: %v", id, recovered))
					}
				}
			}()
			defer func() {
				<-sem
				wg.Done()
			}()

			claim, claimed, claimErr := d.coordinator.TryClaimRoot(ctx, d.cfg.OrganizationID, id)
			if claimErr != nil {
				d.resumeErrors.Add(1)
				d.mu.Lock()
				d.backoffs[id] = time.Now().Add(d.cfg.ErrorBackoff)
				d.mu.Unlock()
				if d.observe != nil {
					d.observe(id, ResultInfraFailure, claimErr)
				}
				return
			}
			if !claimed {
				d.resumeBusy.Add(1)
				if d.observe != nil {
					d.observe(id, ResultBusy, nil)
				}
				return
			}
			defer func() {
				if releaseErr := claim.Release(context.Background()); releaseErr != nil {
					if d.observe != nil {
						d.observe(id, ResultInfraFailure, releaseErr)
					}
				}
			}()

			d.rootsClaimed.Add(1)
			d.resumeCalls.Add(1)

			run, resumeErr := d.resumer.ResumeDurable(ctx, id)
			classification := ClassifyResult(run, resumeErr)

			switch classification {
			case ResultContinue:
				d.resumeSuccess.Add(1)
				d.mu.Lock()
				delete(d.backoffs, id)
				d.mu.Unlock()
			case ResultTerminal:
				d.resumeTerminal.Add(1)
				d.mu.Lock()
				d.backoffs[id] = time.Now().Add(24 * time.Hour)
				d.mu.Unlock()
			case ResultBusy:
				d.resumeBusy.Add(1)
			case ResultBlockedHuman, ResultBlockedSafety:
				d.resumeBlocked.Add(1)
				d.mu.Lock()
				d.backoffs[id] = time.Now().Add(time.Hour)
				d.mu.Unlock()
			case ResultRetryLater:
				d.resumeRetryLater.Add(1)
				d.mu.Lock()
				d.backoffs[id] = time.Now().Add(d.cfg.ErrorBackoff)
				d.mu.Unlock()
			case ResultInfraFailure:
				d.resumeErrors.Add(1)
				d.mu.Lock()
				d.backoffs[id] = time.Now().Add(d.cfg.ErrorBackoff)
				d.mu.Unlock()
			}

			if d.observe != nil {
				d.observe(id, classification, resumeErr)
			}
		}(rootID)
	}

	wg.Wait()
	return d.SnapshotMetrics(), nil
}

// Run executes the continuous background polling loop with advisory wakeup support.
func (d *CampaignDriver) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		_, err := d.RunOnce(ctx)
		if err != nil {
			if !sleepContext(ctx, d.cfg.ErrorBackoff) {
				return nil
			}
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-d.wakeupCh:
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
