package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	executivebootstrap "github.com/Mireuz13/explorarte-organization/internal/executive/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func runExecutive(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printExecutiveUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "submit":
		return runExecutiveSubmit(args[1:], stdout, stderr)
	case "status":
		return runExecutiveStatus(args[1:], stdout, stderr)
	case "resume":
		return runExecutiveResume(args[1:], stdout, stderr)
	case "worker":
		return runExecutiveWorker(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printExecutiveUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown executive command %q\n", args[0])
		printExecutiveUsage(stderr)
		return exitUsage
	}
}

func runExecutiveSubmit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive submit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "owner goal JSON file")
	actorRole := flags.String("actor-role", "", "requesting owner role")
	idempotencyKey := flags.String("idempotency-key", "", "stable owner request key")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *file == "" || *actorRole == "" || *idempotencyKey == "" {
		fmt.Fprintln(stderr, "usage: orgctl executive submit --file goal.json --actor-role empresa/human --idempotency-key KEY [--json]")
		return exitUsage
	}
	goal, err := readExecutiveGoal(*file)
	if err != nil {
		fmt.Fprintf(stderr, "read executive goal: %v\n", err)
		return exitInvalid
	}
	cfg, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-submit", 45*time.Second)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	_ = cfg
	run, reused, err := runtime.Orchestrator.Submit(ctx, executive.SubmitRequest{Goal: goal, ActorRoleID: *actorRole, IdempotencyKey: *idempotencyKey})
	if err != nil {
		fmt.Fprintf(stderr, "submit executive run: %v\n", err)
		return executiveExitCode(err)
	}
	writeExecutiveValue(stdout, *jsonOutput, map[string]any{"run": run, "reused": reused})
	return exitOK
}

func runExecutiveStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: orgctl executive status ROOT_TASK_ID [--json]")
		return exitUsage
	}
	rootID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || rootID <= 0 {
		fmt.Fprintln(stderr, "ROOT_TASK_ID must be a positive integer")
		return exitUsage
	}
	_, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-status", 30*time.Second)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	run, err := runtime.Orchestrator.Status(ctx, rootID)
	if err != nil {
		fmt.Fprintf(stderr, "executive status: %v\n", err)
		return executiveExitCode(err)
	}
	writeExecutiveValue(stdout, *jsonOutput, run)
	return exitOK
}

func runExecutiveResume(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive resume", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: orgctl executive resume ROOT_TASK_ID [--json]")
		return exitUsage
	}
	rootID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || rootID <= 0 {
		fmt.Fprintln(stderr, "ROOT_TASK_ID must be a positive integer")
		return exitUsage
	}
	_, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-resume", 45*time.Second)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	run, err := runtime.Orchestrator.ResumeDurable(ctx, rootID)
	writeExecutiveValue(stdout, *jsonOutput, run)
	if err != nil {
		fmt.Fprintf(stderr, "executive resume: %v\n", err)
		return executiveExitCode(err)
	}
	return exitOK
}

func runExecutiveWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(stderr, "usage: orgctl executive worker run [--poll 1s] [--error-backoff 3s] [--batch 16]")
		return exitUsage
	}
	flags := flag.NewFlagSet("executive worker run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	poll := flags.Duration("poll", time.Second, "poll interval")
	errorBackoff := flags.Duration("error-backoff", 3*time.Second, "source error backoff")
	batch := flags.Int("batch", 16, "maximum roots per poll")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *poll <= 0 || *errorBackoff <= 0 || *batch <= 0 || *batch > 128 {
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, code := openExecutiveDatabase(ctx, cfg, stderr, "executive-worker")
	if code != exitOK {
		return code
	}
	defer store.Close()
	runtime, err := executivebootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "open executive runtime: %v\n", err)
		return exitInternal
	}
	rootSource := runtimeadapter.Tasks{Service: runtime.Tasks, OrganizationID: cfg.Tasks.OrganizationID}
	worker, err := executive.NewWorker(runtime.Orchestrator, rootSource, executive.WorkerConfig{PollInterval: *poll, ErrorBackoff: *errorBackoff, BatchSize: *batch})
	if err != nil {
		fmt.Fprintf(stderr, "create executive worker: %v\n", err)
		return exitInternal
	}
	fmt.Fprintln(stdout, "executive worker started")
	if err = worker.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "executive worker: %v\n", err)
		return exitInternal
	}
	fmt.Fprintln(stdout, "executive worker stopped")
	return exitOK
}

func readExecutiveGoal(path string) (executive.OwnerGoal, error) {
	file, err := os.Open(path)
	if err != nil {
		return executive.OwnerGoal{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 256<<10))
	decoder.DisallowUnknownFields()
	var goal executive.OwnerGoal
	if err = decoder.Decode(&goal); err != nil {
		return executive.OwnerGoal{}, err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return executive.OwnerGoal{}, fmt.Errorf("multiple top-level JSON values")
		}
		return executive.OwnerGoal{}, err
	}
	return goal, nil
}

func openExecutiveRuntime(stderr io.Writer, suffix string, timeout time.Duration) (config.Config, *executivebootstrap.Runtime, *platformpostgres.Store, context.Context, context.CancelFunc, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, nil, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	store, code := openExecutiveDatabase(ctx, cfg, stderr, suffix)
	if code != exitOK {
		cancel()
		return cfg, nil, nil, nil, func() {}, code
	}
	runtime, err := executivebootstrap.Open(cfg, store)
	if err != nil {
		store.Close()
		cancel()
		fmt.Fprintf(stderr, "open executive runtime: %v\n", err)
		return cfg, nil, nil, nil, func() {}, exitInternal
	}
	return cfg, runtime, store, ctx, cancel, exitOK
}

func openExecutiveDatabase(ctx context.Context, cfg config.Config, stderr io.Writer, suffix string) (*platformpostgres.Store, int) {
	store, err := platformpostgres.Open(ctx, cfg.Database, cfg.App.Name+"-"+suffix)
	if err != nil {
		fmt.Fprintf(stderr, "open PostgreSQL: %v\n", err)
		return nil, exitDatabase
	}
	if err = platformpostgres.PingWithTimeout(ctx, store, cfg.Database.ConnectTimeout); err != nil {
		store.Close()
		fmt.Fprintf(stderr, "PostgreSQL unavailable: %v\n", err)
		return nil, exitDatabase
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "create migration runner: %v\n", err)
		return nil, exitInternal
	}
	status, err := runner.Status(ctx)
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return nil, exitInternal
	}
	if !status.Ready {
		store.Close()
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return nil, exitDrift
	}
	return store, exitOK
}

func executiveExitCode(err error) int {
	switch {
	case errors.Is(err, executive.ErrCompletionFailed):
		return exitCompletionFailed
	case errors.Is(err, executive.ErrCompletionInconclusive):
		return exitCompletionInconclusive
	case errors.Is(err, executive.ErrDispatchAssignmentRequired):
		return exitApprovalRequired
	case errors.Is(err, executive.ErrInvalidInput), errors.Is(err, executive.ErrContractRejected), errors.Is(err, executive.ErrForbiddenField):
		return exitInvalid
	default:
		return exitInternal
	}
}

func writeExecutiveValue(out io.Writer, jsonOutput bool, value any) {
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(value)
		return
	}
	if run, ok := value.(executive.Run); ok {
		fmt.Fprintf(out, "root_task_id=%d state=%s correlation_id=%s", run.RootTaskID, run.State, run.CorrelationID)
		if run.ReasonCode != "" {
			fmt.Fprintf(out, " reason_code=%s", run.ReasonCode)
		}
		fmt.Fprintln(out)
		if run.AnswerToOwner != "" {
			fmt.Fprintln(out, run.AnswerToOwner)
		}
		return
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func printExecutiveUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl executive <command> [options]
commands:
  submit --file goal.json --actor-role empresa/human --idempotency-key KEY [--json]
  status ROOT_TASK_ID [--json]
  resume ROOT_TASK_ID [--json]
  worker run [--poll 1s] [--error-backoff 3s] [--batch 16]`)
}
