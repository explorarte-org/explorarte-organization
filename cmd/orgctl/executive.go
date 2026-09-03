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
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// executiveModelCallDeadline bounds any executive CLI verb that may drive a
// real model call (submit/resume/reconcile-gating). It replaces a hardcoded
// 45s deadline that routinely raced real provider completion time (RECON-001):
// read-only, pre-fix production data (model_provider_outcomes.request_duration_ms,
// 2026-09-02) showed deepseek at p99=242.7s/max=245.0s and gemini at
// p99=77.1s/max=138.1s -- both already several times past 45s on real,
// successful calls, not failures. 12 minutes gives headroom above the
// largest adapter-level HTTP timeout this fix also raises (openai_responses,
// see its config.go) plus the deepseek precedent it already matches
// (ORG_MODEL_PROVIDER_DEEPSEEK_REQUEST_TIMEOUT=10m in compose.yaml), with
// margin for host-side work (context assembly, DB writes) around the
// provider call itself.
const executiveModelCallDeadline = 12 * time.Minute

func runExecutive(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printExecutiveUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "submit":
		return runExecutiveSubmit(args[1:], stdout, stderr)
	case "external-smoke":
		return runExecutiveExternalSmoke(args[1:], stdout, stderr)
	case "status":
		return runExecutiveStatus(args[1:], stdout, stderr)
	case "resume":
		return runExecutiveResume(args[1:], stdout, stderr)
	case "worker":
		return runExecutiveWorker(args[1:], stdout, stderr)
	case "reconcile-gating":
		return runExecutiveReconcileGating(args[1:], stdout, stderr)
	case "smoke":
		return runExecutiveSmoke(args[1:], stdout, stderr)
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
	// The campaign's ceilings are stated here, at the moment the campaign is
	// created, and are recorded durably with its root. They are flags rather
	// than environment because the environment is what made the effective
	// budget depend on which process submitted: two processes, two
	// configurations, and a durable row that keeps whichever arrived first.
	defaults := executive.DefaultCampaignBudget()
	maxUSD := flags.Float64("max-usd", defaults.MaxUSD.USD(), "campaign USD ceiling, recorded durably at submission")
	maxTokens := flags.Int64("max-tokens", defaults.MaxTokens, "campaign token ceiling, recorded durably at submission")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *file == "" || *actorRole == "" || *idempotencyKey == "" {
		fmt.Fprintln(stderr, "usage: orgctl executive submit --file goal.json --actor-role empresa/human --idempotency-key KEY [--max-usd 5] [--max-tokens 500000] [--json]")
		return exitUsage
	}
	budget := defaults
	budget.MaxUSD = modelpricing.USDFromDollars(*maxUSD)
	budget.MaxTokens = *maxTokens
	if err := budget.Validate(); err != nil {
		fmt.Fprintf(stderr, "invalid campaign budget: %v\n", err)
		return exitUsage
	}
	goal, err := readExecutiveGoal(*file)
	if err != nil {
		fmt.Fprintf(stderr, "read executive goal: %v\n", err)
		return exitInvalid
	}
	cfg, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-submit", executiveModelCallDeadline)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	_ = cfg
	run, reused, err := runtime.Orchestrator.Submit(ctx, executive.SubmitRequest{Goal: goal, ActorRoleID: *actorRole, IdempotencyKey: *idempotencyKey, Budget: &budget})
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
	_, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-resume", executiveModelCallDeadline)
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
	// The worker's own error handling assumes model executions get
	// reconciled -- it skips unresolved provider-side executions expecting
	// them to settle. Nothing ran that sweep: it existed only as
	// `orgctl model invocation reconcile`, so a stranded invocation stayed
	// stranded and every pass skipped it again. Wiring it here is what makes
	// the assumption true in the deployment, not just in the code.
	worker, err := executive.NewWorker(runtime.Orchestrator, rootSource,
		executive.WorkerConfig{PollInterval: *poll, ErrorBackoff: *errorBackoff, BatchSize: *batch},
		executive.WithExecutionReconciler(runtimeadapter.ExecutionReconciler{Invocations: runtime.Models.Invocations}),
		executive.WithFailureObserver(func(rootTaskID int64, err error) {
			fmt.Fprintf(stderr, "executive worker: root %d: %v\n", rootTaskID, err)
		}))
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

func openExecutiveRuntime(stderr io.Writer, suffix string, timeout time.Duration, options ...executivebootstrap.OpenOption) (config.Config, *executivebootstrap.Runtime, *platformpostgres.Store, context.Context, context.CancelFunc, int) {
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
	runtime, err := executivebootstrap.Open(cfg, store, options...)
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

// runExecutiveReconcileGating manually triggers
// Orchestrator.ReconcileGatedCompletions — recovering tasks whose attempt
// finished and had its decision durably recorded, but where the process
// died before the task itself was finalized/blocked. There is no autonomous
// scheduler in this system yet, so this is operator/cron-triggered, the same
// way orgctl task reconcile, orgctl sleep run, and orgctl postrun already are.
func runExecutiveReconcileGating(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive reconcile-gating", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 100, "maximum tasks to reconcile in one call")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl executive reconcile-gating [--limit 100] [--json]")
		return exitUsage
	}
	_, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-reconcile-gating", executiveModelCallDeadline)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	result, err := runtime.Orchestrator.ReconcileGatedCompletions(ctx, *limit)
	writeExecutiveValue(stdout, *jsonOutput, result)
	if err != nil {
		fmt.Fprintf(stderr, "executive reconcile-gating: %v\n", err)
		return executiveExitCode(err)
	}
	return exitOK
}

func printExecutiveUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl executive <command> [options]
commands:
  submit --file goal.json --actor-role empresa/human --idempotency-key KEY [--json]
  external-smoke --confirm EXECUTIVE_EXTERNAL_SMOKE_ONCE --idempotency-key external-smoke-KEY [--json]
  status ROOT_TASK_ID [--json]
  resume ROOT_TASK_ID [--json]
  worker run [--poll 1s] [--error-backoff 3s] [--batch 16]
  reconcile-gating [--limit 100] [--json]`)
}
