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

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/campaign/financeworker"
	campaignpostgres "github.com/Mireuz13/explorarte-organization/internal/campaign/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	executivebootstrap "github.com/Mireuz13/explorarte-organization/internal/executive/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executive/driver"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelruntimepostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
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
	case "external-smoke-5usd":
		return runExecutiveExternalSmoke5USD(args[1:], stdout, stderr)
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
	case "chat":
		return runExecutiveChat(args[1:], stdout, stderr)
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
	maxModelCalls := flags.Int64("max-model-calls", defaults.MaxModelCalls, "campaign model-call ceiling, recorded durably at submission")
	noRetries := flags.Bool("no-retries", false, "pin every task to one attempt for a bounded operator-run campaign")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *file == "" || *actorRole == "" || *idempotencyKey == "" {
		fmt.Fprintln(stderr, "usage: orgctl executive submit --file goal.json --actor-role empresa/human --idempotency-key KEY [--max-usd 5] [--max-tokens 500000] [--max-model-calls 100] [--no-retries] [--json]")
		return exitUsage
	}
	budget := defaults
	budget.MaxUSD = modelpricing.USDFromDollars(*maxUSD)
	budget.MaxTokens = *maxTokens
	budget.MaxModelCalls = *maxModelCalls
	if *noRetries {
		// MaxRetries remains positive because the budget schema is deliberately
		// non-zero, while WithNoRetries below prevents the task engine from
		// creating a second attempt.
		budget.MaxRetries = 1
	}
	if err := budget.Validate(); err != nil {
		fmt.Fprintf(stderr, "invalid campaign budget: %v\n", err)
		return exitUsage
	}
	goal, err := readExecutiveGoal(*file)
	if err != nil {
		fmt.Fprintf(stderr, "read executive goal: %v\n", err)
		return exitInvalid
	}
	limits := executive.DefaultLimits()
	if *maxModelCalls > int64(^uint(0)>>1) {
		fmt.Fprintln(stderr, "invalid campaign budget: max-model-calls exceeds the local integer range")
		return exitUsage
	}
	limits.MaxModelCalls = int(*maxModelCalls)
	options := []executivebootstrap.OpenOption{executivebootstrap.WithExecutiveLimits(limits)}
	if *noRetries {
		options = append(options, executivebootstrap.WithNoRetries())
	}
	cfg, runtime, store, ctx, cancel, code := openExecutiveRuntime(stderr, "executive-submit", executiveModelCallDeadline, options...)
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
	writeExecutiveValue(stdout, *jsonOutput, map[string]any{
		"run": run, "reused": reused, "max_model_calls": *maxModelCalls,
		"no_retries": *noRetries,
	})
	return exitOK
}

func runExecutiveStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: orgctl executive status ROOT_TASK_ID [--json]")
		return exitUsage
	}
	rootID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || rootID <= 0 {
		fmt.Fprintln(stderr, "ROOT_TASK_ID must be a positive integer")
		return exitUsage
	}
	cfg, taskService, cleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, code := openExecutiveDatabase(ctx, cfg, stderr, "executive-status")
	if code != exitOK {
		return code
	}
	defer store.Close()
	results, err := modelruntimepostgres.New(store)
	if err != nil {
		return exitInternal
	}
	run, err := executive.ReadStatus(ctx,
		runtimeadapter.Tasks{Service: taskService, OrganizationID: cfg.Tasks.OrganizationID},
		runtimeadapter.StoredResults{Store: results, OrganizationID: cfg.Tasks.OrganizationID}, rootID, executive.DefaultLimits())
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
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 1 {
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
		fmt.Fprintln(stderr, "usage: orgctl executive worker run [--poll 2s] [--error-backoff 3s] [--batch 16] [--max-concurrency 4]")
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	flags := flag.NewFlagSet("executive worker run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	poll := flags.Duration("poll", cfg.ExecutiveDriver.PollInterval, "poll interval")
	errorBackoff := flags.Duration("error-backoff", cfg.ExecutiveDriver.ErrorBackoff, "source error backoff")
	batch := flags.Int("batch", cfg.ExecutiveDriver.BatchSize, "maximum roots per poll")
	maxConcurrency := flags.Int("max-concurrency", cfg.ExecutiveDriver.MaxConcurrency, "maximum concurrent roots per replica")
	noRetries := flags.Bool("no-retries", false, "pin every task to one attempt for this operator-run campaign")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 ||
		*poll < 100*time.Millisecond || *poll > 10*time.Minute ||
		*errorBackoff < 100*time.Millisecond ||
		*batch <= 0 || *batch > 128 ||
		*maxConcurrency <= 0 || *maxConcurrency > 32 {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, code := openExecutiveDatabase(ctx, cfg, stderr, "executive-worker")
	if code != exitOK {
		return code
	}
	defer store.Close()
	options := make([]executivebootstrap.OpenOption, 0, 1)
	if *noRetries {
		options = append(options, executivebootstrap.WithNoRetries())
	}
	runtime, err := executivebootstrap.Open(cfg, store, options...)
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
	coord := driver.NewPostgresRootCoordinator(store.Pool())
	driverCfg := driver.Config{
		OrganizationID: cfg.Tasks.OrganizationID,
		PollInterval:   *poll,
		ErrorBackoff:   *errorBackoff,
		BatchSize:      *batch,
		MaxConcurrency: *maxConcurrency,
	}
	drv, err := driver.NewCampaignDriver(
		runtime.Orchestrator,
		rootSource,
		coord,
		driverCfg,
		driver.WithExecutionReconciler(runtimeadapter.ExecutionReconciler{Invocations: runtime.Models.Invocations}),
		driver.WithObserver(func(rootTaskID int64, classification driver.ResultClassification, err error) {
			if err != nil && classification != driver.ResultBusy && classification != driver.ResultBlockedHuman {
				fmt.Fprintf(stderr, "executive worker: root %d [%s]: %v\n", rootTaskID, classification, err)
			}
		}),
	)
	if err != nil {
		fmt.Fprintf(stderr, "create executive worker: %v\n", err)
		return exitInternal
	}

	finWorker, err := buildFinanceWorker(ctx, cfg, store, runtime, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "create finance worker: %v\n", err)
		return exitInternal
	}

	fmt.Fprintln(stdout, "executive worker started")
	// The autonomous campaign driver and the finance review worker run as
	// two goroutines of the SAME process, sharing runtime.Models (one
	// Model Runtime, opened once above) -- not two worker processes, not
	// a second composition root. Neither is optional: superviseExecutiveWorkers
	// fails the whole process fast if either one terminates unexpectedly
	// (error, or even a premature "successful" nil) while ctx is still
	// active, cancelling the sibling immediately rather than leaving it
	// running alone forever -- a worker process silently missing half its
	// job is worse than a visible, restart-recoverable crash (this
	// process already runs under Restart=always).
	if runErr := superviseExecutiveWorkers(ctx, drv.Run, finWorker.Run); runErr != nil {
		fmt.Fprintf(stderr, "executive worker: %v\n", runErr)
		return exitInternal
	}
	fmt.Fprintln(stdout, "executive worker stopped")
	return exitOK
}

// financeTaskCoordinator adapts *tasks.Service to campaign.TaskCoordinator.
// *tasks.Service already implements every method that interface needs
// (CreateTask, ClaimTaskByID, StartAttempt, RecordAttemptResult,
// FinalizeTask) with an identical signature except GetTask, which returns
// the richer tasks.TaskDetail rather than the bare tasks.Task
// campaign.TaskCoordinator expects. Duplicated from the identical adapter
// in internal/ceochat/bootstrap/runtime.go (unexported there, in a
// different package) rather than shared -- the same precedent this
// codebase already established for this exact shape.
type financeTaskCoordinator struct {
	*tasks.Service
}

func (f financeTaskCoordinator) GetTask(ctx context.Context, taskID int64) (tasks.Task, error) {
	detail, err := f.Service.GetTask(ctx, taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	return detail.Task, nil
}

// financeDispatchProvisioner adapts
// *modeldispatch.AuthorizedAttemptProvisioner's (CreateAssignmentResult,
// error) return to the bare error campaign.DispatchProvisioner expects --
// the finance worker only needs to know whether it may proceed to the
// Harness, never the assignment's own identity or quota. Same shape as
// ceochat's own dispatchProvisioner adapter.
type financeDispatchProvisioner struct {
	provisioner *modeldispatch.AuthorizedAttemptProvisioner
}

func (d financeDispatchProvisioner) EnsureAuthorizedAssignmentForRunningAttempt(ctx context.Context, taskID, attemptID int64) error {
	_, err := d.provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, taskID, attemptID)
	return err
}

// buildFinanceWorker wires the autonomous consumer of
// campaign.financial_review tasks into this SAME worker process,
// reusing the Model Runtime, task service, and database connection
// executivebootstrap.Open already opened above -- never a second
// modelbootstrap.Open call. This is the missing link between
// campaign.request_financial_review (which only ever creates a task,
// deliberately never blocking a CEO chat turn on model completion) and
// FinanceService.ExecuteReviewTask (fully built, crash-safe, race-safe,
// but with zero production callers before this).
func buildFinanceWorker(ctx context.Context, cfg config.Config, store *platformpostgres.Store, runtime *executivebootstrap.Runtime, stderr io.Writer) (*financeworker.Worker, error) {
	campaignStore, err := campaignpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("open campaign store: %w", err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("open authorization store: %w", err)
	}
	authorizerPolicy, err := authorization.NewWithPolicyReader(authorizationStore, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("open capability authorizer: %w", err)
	}
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return nil, fmt.Errorf("open registry repository: %w", err)
	}
	// Reviewer role resolution is canonical, not a hardcoded string:
	// DefaultReviewerRoleResolver searches negocio's own roles for one
	// holding campaign.financial_review.perform, falling back to the
	// canonical constant only if the authorizer itself confirms that role
	// still holds the capability.
	roleResolver := campaign.DefaultReviewerRoleResolver{Registry: registryRepository, Authorizer: authorizerPolicy}
	revision, err := registryRepository.GetCurrentRevision(ctx, cfg.Tasks.OrganizationID)
	if err != nil || revision == nil {
		return nil, fmt.Errorf("read current organization revision: revision=%+v err=%w", revision, err)
	}
	reviewerRoleID, err := roleResolver.ResolveReviewerRole(ctx, cfg.Tasks.OrganizationID, revision.ID)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical finance reviewer role: %w", err)
	}

	harnessAuthority, err := runtime.Models.NewHarnessAuthority()
	if err != nil {
		return nil, fmt.Errorf("create harness authority: %w", err)
	}
	harnessHistory, err := executionharnesspostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create harness history store: %w", err)
	}
	// Finance performs exactly one bounded model review per task (the
	// Harness spec itself pins MaxTurns=1) -- the provisioner's own
	// default MaxInvocations=1 is left untouched, never overridden the
	// way ceochat overrides it to 8 for its own multi-turn conversation
	// shape.
	financeAssignments, err := runtime.Models.Dispatcher.NewAuthorizedAttemptProvisioner(runtime.Models.Config.ExecutionPrincipalKey)
	if err != nil {
		return nil, fmt.Errorf("create finance dispatch provisioner: %w", err)
	}
	// The role-bound principal for the finance reviewer role -- never
	// empresa/ceo, empresa/human, or a generic technical principal
	// presented as Finance. The SAME identity holds the task lease, owns
	// the dispatch assignment, and executes the Harness run: it is
	// threaded through once here as HolderPrincipalID, and
	// ExecuteReviewTask itself reuses it for all three.
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(runtime.Models.Dispatcher.Store, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create role-bound principal resolver: %w", err)
	}
	financePrincipal, err := roleBoundResolver.Resolve(ctx, reviewerRoleID)
	if err != nil {
		return nil, fmt.Errorf("resolve finance role-bound principal: %w", err)
	}
	holderPrincipalID := strconv.FormatInt(financePrincipal.ID, 10)

	financeService, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID:  cfg.Tasks.OrganizationID,
		Store:           campaignStore,
		Tasks:           financeTaskCoordinator{runtime.Tasks},
		Assignments:     financeDispatchProvisioner{financeAssignments},
		Authorizer:      authorizerPolicy,
		RoleResolver:    roleResolver,
		Authority:       harnessAuthority,
		HarnessHistory:  harnessHistory,
		DescriptorStore: harnessHistory,
		NewModelExecutor: func(execConfig modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return runtime.Models.NewHarnessModelExecutor(execConfig)
		},
		WorkerID:          "executive-worker-finance",
		HolderPrincipalID: holderPrincipalID,
	})
	if err != nil {
		return nil, fmt.Errorf("create finance service: %w", err)
	}

	discovery := financeworker.DiscoveryTaskSource{
		Service: runtime.Tasks, OrganizationID: cfg.Tasks.OrganizationID, ReviewerRoleID: reviewerRoleID,
	}
	workerCfg := financeworker.DefaultConfig(cfg.Tasks.OrganizationID)
	workerCfg.WorkerID = "executive-worker-finance"
	workerCfg.HolderPrincipalID = holderPrincipalID

	return financeworker.NewWorker(discovery, campaignStore, financeService, workerCfg,
		financeworker.WithObserver(func(taskID int64, classification financeworker.ResultClassification, err error) {
			if err != nil && classification != financeworker.ResultBusy {
				fmt.Fprintf(stderr, "executive worker: finance task %d [%s]: %v\n", taskID, classification, err)
			}
		}),
	)
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
  external-smoke-5usd --confirm EXECUTIVE_EXTERNAL_SMOKE_5USD_ONCE --idempotency-key external-smoke-5usd-KEY [--json]
  status ROOT_TASK_ID [--json]
  resume ROOT_TASK_ID [--json]
  worker run [--poll 2s] [--error-backoff 3s] [--batch 16] [--max-concurrency 4]
  reconcile-gating [--limit 100] [--json]
  chat create --actor-role empresa/human [--json]
  chat send CONVERSATION_ID --actor-role empresa/human --idempotency-key KEY [--file message.txt] [--json]
  chat history CONVERSATION_ID [--limit 32] [--json]`)
}
