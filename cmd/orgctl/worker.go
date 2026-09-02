package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/cellworker"
	cellworkerpostgres "github.com/Mireuz13/explorarte-organization/internal/cellworker/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
	"github.com/Mireuz13/explorarte-organization/internal/platform/httpserver"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// runModelWorker implements `orgctl model worker run`: a persistent process
// that polls for invocations pinned to ORG_MODEL_EXECUTION_PRINCIPAL_KEY and
// dispatches them until SIGINT/SIGTERM, then drains in-flight dispatch
// before exiting. It is a separate, explicitly-launched process — cmd/orgd
// and internal/app are untouched, so orgd still never dispatches.
func runModelWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(stderr, "usage: orgctl model worker run")
		return exitUsage
	}
	flags := flag.NewFlagSet("model worker run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
		return exitUsage
	}

	cfg, dbStore, runtime, cleanup, code := openModelWorkerRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()

	if !runtime.Config.Enabled {
		fmt.Fprintln(stderr, "model runtime is disabled (ORG_MODEL_RUNTIME_ENABLED)")
		return exitUsage
	}

	workerCfg, err := cellworker.LoadConfig(os.LookupEnv, runtime.Config.ExecutionPrincipalKey)
	if err != nil {
		fmt.Fprintf(stderr, "load worker configuration: %v\n", err)
		return exitUsage
	}
	workSource, err := cellworkerpostgres.New(dbStore, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "open worker work source: %v\n", err)
		return exitInternal
	}
	worker, err := cellworker.New(workerCfg, workSource, runtime.Dispatch, nil, &stderrObserver{w: stderr})
	if err != nil {
		fmt.Fprintf(stderr, "construct worker: %v\n", err)
		return exitUsage
	}

	// A real health/readiness endpoint, not a pgrep-based Docker healthcheck:
	// this process runs in the same distroless, shell-less image as orgd, so
	// a CMD-SHELL pgrep check (what this compose service originally shipped
	// with) can never execute at all -- found live, model-worker reported
	// unhealthy the entire time despite dispatching real, successful model
	// calls (Wave 5 finding, ORGANIZATION-GRAND-AUDIT-001). Reuses the same
	// internal/platform/httpserver orgd already runs, bound to the same
	// ORG_HTTP_ADDR default -- distinct containers, no port collision -- so
	// the Docker healthcheck can be `orgctl health --ready`, the exact form
	// already proven for orgd, instead of a shell command that can't run
	// here. Readiness reflects real dependency health (a DB ping), not mere
	// process existence -- a strictly stronger signal than the pgrep check
	// it replaces, not just a portable version of it.
	healthServer := httpserver.New(cfg.HTTP, nil, buildinfo.Info{Version: version, Commit: commit, BuildTime: buildTime}, func(readyCtx context.Context) error {
		pingCtx, cancel := context.WithTimeout(readyCtx, cfg.Database.PingTimeout)
		defer cancel()
		return dbStore.Ping(pingCtx)
	})
	healthErrs, err := healthServer.Start()
	if err != nil {
		fmt.Fprintf(stderr, "start health endpoint: %v\n", err)
		return exitInternal
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "model worker starting: principal=%s batch=%d concurrency=%d\n", workerCfg.PrincipalKey, workerCfg.BatchSize, workerCfg.Concurrency)
	runErr := worker.Run(ctx)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if shutdownErr := healthServer.Shutdown(shutdownCtx); shutdownErr != nil {
		fmt.Fprintf(stderr, "shut down health endpoint: %v\n", shutdownErr)
	}
	select {
	case healthErr := <-healthErrs:
		if healthErr != nil {
			fmt.Fprintf(stderr, "health endpoint stopped with error: %v\n", healthErr)
		}
	default:
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		fmt.Fprintf(stderr, "model worker stopped with error: %v\n", runErr)
		return exitInternal
	}
	fmt.Fprintln(stdout, "model worker stopped")
	return exitOK
}

// stderrObserver is the production cellworker.Observer: it makes list and
// dispatch failures visible on the process's own stderr, since durable
// state alone gives an operator watching this process no signal. OnListError
// runs from the poll loop and OnDispatchError from concurrent dispatch
// goroutines, so writes are serialized: the underlying io.Writer is not
// guaranteed thread-safe on its own.
type stderrObserver struct {
	mu sync.Mutex
	w  io.Writer
}

func (o *stderrObserver) OnListError(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.w, "model worker: list eligible invocations failed: %v\n", err)
}

func (o *stderrObserver) OnDispatchError(invocationID int64, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.w, "model worker: dispatch invocation %d failed: %v\n", invocationID, err)
}

func openModelWorkerRuntime(stderr io.Writer) (config.Config, *postgres.Store, *modelbootstrap.Runtime, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.MigrationTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "model-worker")
	if code != exitOK {
		return config.Config{}, nil, nil, func() {}, code
	}
	cleanup := func() { store.Close() }
	status, err := runner.Status(ctx)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return config.Config{}, nil, nil, func() {}, exitInternal
	}
	if !status.Ready {
		cleanup()
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return config.Config{}, nil, nil, func() {}, exitDrift
	}
	runtime, err := modelbootstrap.Open(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open model runtime: %v\n", err)
		return config.Config{}, nil, nil, func() {}, exitInternal
	}
	return cfg, store, runtime, cleanup, exitOK
}
