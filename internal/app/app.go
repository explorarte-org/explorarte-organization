package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
	"github.com/Mireuz13/explorarte-organization/internal/platform/httpserver"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	stagingbootstrap "github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

var (
	ErrHTTPServerStopped = errors.New("HTTP server stopped unexpectedly")
	ErrProcessStarting   = errors.New("organization kernel is still starting")
	ErrSchemaNotReady    = errors.New("database schema is not ready")
	// ErrSchemaStructurallyIncompatible ends the process rather than
	// retrying: no amount of waiting reconciles a binary with a database
	// that has applied migrations it does not contain.
	ErrSchemaStructurallyIncompatible = errors.New("database schema is structurally incompatible with this binary")
)

type Database interface {
	Ping(context.Context) error
	Close()
}

type Migrator interface {
	Up(context.Context) (platformmigrations.Result, error)
	Status(context.Context) (platformmigrations.Status, error)
	// Tip is the highest migration version this binary carries. It is part
	// of the contract because deployment drift is diagnosed by comparing it
	// against the database tip.
	Tip() int64
}

type App struct {
	cfg               config.Config
	logger            *slog.Logger
	database          Database
	migrator          Migrator
	process           atomic.Bool
	schema            atomic.Bool
	server            *httpserver.Server
	reconciler        tasks.TaskReconciler
	stagingReconciler staging.StagingReconciler
	stagingInitErr    error
	// fatal carries a condition that makes this process permanently unable
	// to serve, so Run can exit non-zero instead of idling. See
	// prepareDatabase.
	fatal chan error
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger, info buildinfo.Info) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	store, err := postgres.Open(ctx, cfg.Database, cfg.App.Name)
	if err != nil {
		return nil, err
	}
	migrator, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("create migration runner: %w", err)
	}
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("create registry repository for tasks: %w", err)
	}
	catalog, err := registryadapter.New(registryRepository)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("create task registry adapter: %w", err)
	}
	taskStore, err := taskpostgres.New(store)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("create durable task store: %w", err)
	}
	taskService, err := tasks.NewService(taskStore, catalog, tasks.Config{
		OrganizationID:       cfg.Tasks.OrganizationID,
		DefaultMaxAttempts:   cfg.Tasks.DefaultMaxAttempts,
		DefaultLeaseDuration: cfg.Tasks.DefaultLeaseDuration,
		MaxLeaseDuration:     cfg.Tasks.MaxLeaseDuration,
		RetryPolicy: tasks.RetryPolicy{
			BaseDelay: cfg.Tasks.RetryBaseDelay,
			MaxDelay:  cfg.Tasks.RetryMaxDelay,
		},
		OutboxMaxAttempts:   cfg.Tasks.OutboxMaxAttempts,
		OutboxClaimDuration: cfg.Tasks.OutboxClaimDuration,
	})
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("create durable task service: %w", err)
	}
	application := newWithDependencies(cfg, logger, info, store, migrator)
	application.reconciler = taskService
	if cfg.Staging.Enabled {
		runtime, stagingErr := stagingbootstrap.Open(cfg, store)
		if stagingErr != nil {
			application.stagingInitErr = stagingErr
			logger.Error("staging initialization failed; HTTP health remains live but readiness is blocked", "error", stagingErr)
		} else {
			application.stagingReconciler = runtime.Service
		}
	}
	return application, nil
}

func newWithDependencies(cfg config.Config, logger *slog.Logger, info buildinfo.Info, database Database, migrator Migrator) *App {
	if logger == nil {
		logger = slog.Default()
	}
	// Report the schema this binary carries, so /version answers the
	// deployment-drift question directly instead of requiring log
	// archaeology (see buildinfo.Info.MigrationTip).
	info.MigrationTip = migrator.Tip()
	application := &App{cfg: cfg, logger: logger, database: database, migrator: migrator, fatal: make(chan error, 1)}
	application.server = httpserver.New(cfg.HTTP, logger, info, application.Ready)
	return application
}

func (a *App) Run(ctx context.Context) error {
	errorsCh, err := a.server.Start()
	if err != nil {
		return fmt.Errorf("start HTTP server: %w", err)
	}
	a.process.Store(true)
	a.logger.Info("organization kernel process is live", "http_addr", a.server.Addr())
	backgroundCtx, cancelBackground := context.WithCancel(ctx)
	var backgroundWG sync.WaitGroup
	backgroundWG.Add(1)
	go func() {
		defer backgroundWG.Done()
		a.prepareDatabase(backgroundCtx)
	}()
	if a.cfg.Tasks.ReconcilerEnabled && a.reconciler != nil {
		backgroundWG.Add(1)
		go func() {
			defer backgroundWG.Done()
			a.runTaskReconciler(backgroundCtx)
		}()
	}
	if a.cfg.Staging.Enabled && a.stagingReconciler != nil {
		backgroundWG.Add(1)
		go func() {
			defer backgroundWG.Done()
			a.runStagingReconciler(backgroundCtx)
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		if err != nil {
			runErr = fmt.Errorf("serve HTTP: %w", err)
		} else {
			runErr = ErrHTTPServerStopped
		}
	case err := <-a.fatal:
		runErr = err
	}
	a.process.Store(false)
	a.schema.Store(false)
	cancelBackground()
	backgroundWG.Wait()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.App.ShutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		if runErr != nil {
			runErr = errors.Join(runErr, err)
		} else {
			runErr = err
		}
	}
	if a.database != nil {
		a.database.Close()
	}
	return runErr
}

func (a *App) prepareDatabase(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		attemptCtx, cancel := context.WithTimeout(ctx, a.cfg.Database.MigrationTimeout)
		err := a.ensureSchema(attemptCtx)
		cancel()

		wasReady := a.schema.Swap(err == nil)
		wait := a.cfg.Database.HealthCheckPeriod
		if err == nil {
			if !wasReady {
				a.logger.Info("PostgreSQL schema is ready", "auto_migrate", a.cfg.Database.AutoMigrate)
			}
		} else if errors.Is(err, platformmigrations.ErrStructuralSchema) {
			// The binary and the database disagree about what the schema is.
			// Retrying re-runs an identical comparison and fails identically,
			// so the old five-second retry loop only buried the incident: a
			// real deployment sat live-but-never-ready for days, emitting
			// nothing louder than a warning. Fail loudly and exit; the
			// supervisor already knows how to restart, and a restarting
			// container is visible in a way an idle one is not.
			a.logger.Error("PostgreSQL schema is structurally incompatible with this binary; the process cannot serve and is exiting",
				"error", err, "remediation", "deploy a binary whose migration set matches the database, or correct the database")
			select {
			case a.fatal <- fmt.Errorf("%w: %w", ErrSchemaStructurallyIncompatible, err):
			default:
			}
			return
		} else {
			wait = a.cfg.Database.MigrationRetry
			if wasReady {
				a.logger.Warn("PostgreSQL schema became unavailable; process remains live", "error", err, "retry_in", wait)
			} else {
				a.logger.Warn("PostgreSQL schema is not ready; process remains live", "error", err, "retry_in", wait)
			}
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (a *App) runTaskReconciler(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Tasks.ReconcileInterval)
	defer ticker.Stop()
	for {
		if a.schema.Load() {
			a.reconcileTasksOnce(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) reconcileTasksOnce(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.logger.Error("task reconciler panic recovered; next interval remains scheduled", "panic", recovered)
		}
	}()
	reconcileCtx, cancel := context.WithTimeout(ctx, a.cfg.Tasks.CommandTimeout)
	defer cancel()
	result, err := a.reconciler.Reconcile(reconcileCtx, a.cfg.Tasks.ReconcileBatchSize)
	if err != nil {
		a.logger.Warn("durable task reconciliation failed; process remains live", "error", err)
		return
	}
	if result.ExpiredLeases+result.RecoveredOutbox+result.PromotedTasks+result.BlockedDependencies+result.BlockedAssignees > 0 {
		a.logger.Info("durable task reconciliation completed",
			"expired_leases", result.ExpiredLeases,
			"recovered_outbox", result.RecoveredOutbox,
			"promoted_tasks", result.PromotedTasks,
			"blocked_dependencies", result.BlockedDependencies,
			"blocked_assignees", result.BlockedAssignees,
		)
	}
}

func (a *App) runStagingReconciler(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Staging.ReconcileInterval)
	defer ticker.Stop()
	for {
		if a.schema.Load() {
			a.reconcileStagingOnce(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) reconcileStagingOnce(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.logger.Error("staging reconciler panic recovered; next interval remains scheduled", "panic", recovered)
		}
	}()
	reconcileCtx, cancel := context.WithTimeout(ctx, a.cfg.Staging.CommandTimeout)
	defer cancel()
	result, err := a.stagingReconciler.Reconcile(reconcileCtx, a.cfg.Staging.ReconcileBatchSize)
	if err != nil {
		a.logger.Warn("staging reconciliation failed; process remains live", "error", err)
		return
	}
	if result.RecoveredWorkspaces+result.MissingWorkspaces+result.QuarantinedDirectories+result.RecoveredPromotions+result.ConflictedPromotions+result.RecoveredCleanup > 0 {
		a.logger.Info("staging reconciliation completed", "recovered_workspaces", result.RecoveredWorkspaces, "missing_workspaces", result.MissingWorkspaces, "quarantined_directories", result.QuarantinedDirectories, "recovered_promotions", result.RecoveredPromotions, "conflicted_promotions", result.ConflictedPromotions, "recovered_cleanup", result.RecoveredCleanup)
	}
}

func (a *App) ensureSchema(ctx context.Context) error {
	if a.database == nil || a.migrator == nil {
		return errors.New("database dependencies are not configured")
	}
	if err := a.database.Ping(ctx); err != nil {
		return err
	}
	if a.cfg.Database.AutoMigrate {
		_, err := a.migrator.Up(ctx)
		return err
	}
	status, err := a.migrator.Status(ctx)
	if err != nil {
		return err
	}
	if !status.Ready {
		return fmt.Errorf("database has %d pending migrations", status.Pending)
	}
	return nil
}

func (a *App) Ready(ctx context.Context) error {
	if !a.process.Load() {
		return ErrProcessStarting
	}
	if !a.schema.Load() {
		return ErrSchemaNotReady
	}
	if a.cfg.Staging.Enabled && a.stagingInitErr != nil {
		return fmt.Errorf("staging is not ready: %w", a.stagingInitErr)
	}
	if a.database == nil || a.migrator == nil {
		return errors.New("PostgreSQL dependencies are not configured")
	}
	readyCtx, cancel := context.WithTimeout(ctx, a.cfg.Database.PingTimeout)
	defer cancel()
	if err := a.database.Ping(readyCtx); err != nil {
		return err
	}
	status, err := a.migrator.Status(readyCtx)
	if err != nil {
		a.schema.Store(false)
		return err
	}
	if !status.Ready {
		a.schema.Store(false)
		return fmt.Errorf("database has %d pending migrations", status.Pending)
	}
	return nil
}

func (a *App) Addr() string {
	return a.server.Addr()
}
