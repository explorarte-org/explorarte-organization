package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphtrace"
	"github.com/Mireuz13/explorarte-organization/internal/executive/postrun"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
)

func runPostrun(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPostrunUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "postrun")
	if code != exitOK {
		return code
	}
	defer store.Close()
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return exitInternal
	}
	if !status.Ready {
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return exitDrift
	}

	switch args[0] {
	case "propose-lesson":
		jsonOutput, runID, code := parseDecisionRunID(args[1:], stderr)
		if code != exitOK {
			return code
		}

		traces, err := decisiongraphtrace.New(store, cfg.Tasks.OrganizationID)
		if err != nil {
			fmt.Fprintf(stderr, "create decision trace reader: %v\n", err)
			return exitInternal
		}
		completionReader, err := completionpostgres.New(store, cfg.Tasks.OrganizationID)
		if err != nil {
			fmt.Fprintf(stderr, "create completion reader: %v\n", err)
			return exitInternal
		}
		completionService, err := completion.NewService(completionReader, completionReader, completionReader, completionReader, completionReader, nil)
		if err != nil {
			fmt.Fprintf(stderr, "create completion service: %v\n", err)
			return exitInternal
		}
		registryRepo, err := registry.NewPostgresRepository(store)
		if err != nil {
			fmt.Fprintf(stderr, "create registry repository: %v\n", err)
			return exitInternal
		}
		catalog, err := registryadapter.New(registryRepo)
		if err != nil {
			fmt.Fprintf(stderr, "create task registry adapter: %v\n", err)
			return exitInternal
		}
		taskDB, err := taskpostgres.New(store)
		if err != nil {
			fmt.Fprintf(stderr, "create task store: %v\n", err)
			return exitInternal
		}
		taskService, err := tasks.NewService(taskDB, catalog, tasks.Config{
			OrganizationID:       cfg.Tasks.OrganizationID,
			DefaultMaxAttempts:   cfg.Tasks.DefaultMaxAttempts,
			DefaultLeaseDuration: cfg.Tasks.DefaultLeaseDuration,
			MaxLeaseDuration:     cfg.Tasks.MaxLeaseDuration,
			RetryPolicy:          tasks.RetryPolicy{BaseDelay: cfg.Tasks.RetryBaseDelay, MaxDelay: cfg.Tasks.RetryMaxDelay},
			OutboxMaxAttempts:    cfg.Tasks.OutboxMaxAttempts,
			OutboxClaimDuration:  cfg.Tasks.OutboxClaimDuration,
		})
		if err != nil {
			fmt.Fprintf(stderr, "create task service: %v\n", err)
			return exitInternal
		}
		memoryRuntime, err := memorybootstrap.Open(cfg, store)
		if err != nil {
			fmt.Fprintf(stderr, "create memory runtime: %v\n", err)
			return exitInternal
		}

		service, err := postrun.NewService(traces, completionService, postrun.TaskRoleResolver{Service: taskService}, memoryRuntime.Manager)
		if err != nil {
			fmt.Fprintf(stderr, "create postrun service: %v\n", err)
			return exitInternal
		}
		outcome, err := service.ProcessRun(ctx, cfg.Tasks.OrganizationID, runID)
		if err != nil {
			fmt.Fprintf(stderr, "propose lesson: %v\n", err)
			return exitInternal
		}
		writeValue(stdout, jsonOutput, outcome)
		return exitOK
	default:
		printPostrunUsage(stderr)
		return exitUsage
	}
}

func printPostrunUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl postrun propose-lesson [--json] <run-id>

Reads a terminal decisiongraph run, independently re-verifies its
completion obligations, and proposes a memory candidate for human review
when there is a real, non-pass problem and the task's own role is
authorized to propose one (docs/canonical/capability-matrix.yaml:
memory.propose). Idempotent: re-running for the same run-id is a no-op.`)
}
