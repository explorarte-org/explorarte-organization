package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
)

func runTask(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTaskUsage(stderr)
		return exitUsage
	}
	cfg, service, cleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	args = normalizeTaskSubcommand(args)
	switch args[0] {
	case "create":
		return taskCreate(ctx, service, args[1:], stdout, stderr)
	case "get":
		return taskGet(ctx, service, args[1:], stdout, stderr)
	case "list":
		return taskList(ctx, service, args[1:], stdout, stderr)
	case "claim":
		return taskClaim(ctx, service, cfg, args[1:], stdout, stderr)
	case "claim-specific":
		return taskClaimSpecific(ctx, service, cfg, args[1:], stdout, stderr)
	case "start":
		return taskStart(ctx, service, args[1:], stdout, stderr)
	case "heartbeat":
		return taskHeartbeat(ctx, service, cfg, args[1:], stdout, stderr)
	case "result":
		return taskResult(ctx, service, args[1:], stdout, stderr)
	case "finalize":
		return taskFinalize(ctx, service, args[1:], stdout, stderr)
	case "block":
		return taskBlock(ctx, service, args[1:], stdout, stderr)
	case "unblock":
		return taskUnblock(ctx, service, args[1:], stdout, stderr)
	case "cancel":
		return taskCancel(ctx, service, args[1:], stdout, stderr)
	case "dependency-add":
		return taskDependencyAdd(ctx, service, args[1:], stdout, stderr)
	case "requirement-add":
		return taskRequirementAdd(ctx, service, args[1:], stdout, stderr)
	case "evidence-add":
		return taskEvidenceAdd(ctx, service, args[1:], stdout, stderr)
	case "events":
		return taskEvents(ctx, service, args[1:], stdout, stderr)
	case "attempts":
		return taskAttempts(ctx, service, args[1:], stdout, stderr)
	case "reconcile":
		return taskReconcile(ctx, service, args[1:], stdout, stderr)
	case "dead-list":
		return taskDeadList(ctx, service, args[1:], stdout, stderr)
	case "dead-show":
		return taskDeadShow(ctx, service, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown task command %q\n", args[0])
		printTaskUsage(stderr)
		return exitUsage
	}
}

func runOutbox(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printOutboxUsage(stderr)
		return exitUsage
	}
	cfg, service, cleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	switch args[0] {
	case "claim":
		flags := flag.NewFlagSet("outbox claim", flag.ContinueOnError)
		flags.SetOutput(stderr)
		consumer := flags.String("consumer", "", "consumer identifier")
		batch := flags.Int("batch", 1, "claim batch (1-32)")
		duration := flags.Duration("claim-duration", cfg.Tasks.OutboxClaimDuration, "claim lease duration")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		values, err := service.ClaimOutbox(ctx, tasks.OutboxClaimRequest{ConsumerID: *consumer, BatchSize: *batch, ClaimDuration: *duration})
		if err != nil {
			return taskError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, values)
		return exitOK
	case "ack", "nack":
		flags := flag.NewFlagSet("outbox "+args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		consumer := flags.String("consumer", "", "consumer identifier")
		errorText := flags.String("error", "", "delivery error for nack")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "outbox event")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		token, err := readSecretToken(os.Stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		disposition := tasks.OutboxDisposition{EventID: id, ClaimToken: token, ConsumerID: *consumer, Error: *errorText}
		if args[0] == "ack" {
			err = service.AckOutbox(ctx, disposition)
		} else {
			err = service.NackOutbox(ctx, disposition)
		}
		if err != nil {
			return taskError(stderr, err)
		}
		fmt.Fprintf(stdout, "outbox event %d %sed\n", id, args[0])
		return exitOK
	case "recover":
		flags := flag.NewFlagSet("outbox recover", flag.ContinueOnError)
		flags.SetOutput(stderr)
		batch := flags.Int("batch", 100, "recovery batch")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		count, err := service.RecoverOutbox(ctx, *batch)
		if err != nil {
			return taskError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, map[string]any{"recovered": count})
		return exitOK
	case "status":
		jsonOutput, rest, code := parseJSONOnly("outbox status", args[1:], stderr)
		if code != exitOK || len(rest) != 0 {
			return exitUsage
		}
		stats, err := service.OutboxStats(ctx)
		if err != nil {
			return taskError(stderr, err)
		}
		writeValue(stdout, jsonOutput, stats)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown outbox command %q\n", args[0])
		printOutboxUsage(stderr)
		return exitUsage
	}
}

func openTaskService(stderr io.Writer) (config.Config, *tasks.Service, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "tasks")
	if code != exitOK {
		return config.Config{}, nil, func() {}, code
	}
	cleanup := func() { store.Close() }
	status, err := runner.Status(ctx)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	if !status.Ready {
		cleanup()
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return config.Config{}, nil, func() {}, exitDrift
	}
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "create registry repository: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	catalog, err := registryadapter.New(registryRepository)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "create task registry adapter: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	taskStore, err := taskpostgres.New(store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "create task store: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	service, err := tasks.NewService(taskStore, catalog, tasks.Config{
		OrganizationID:       cfg.Tasks.OrganizationID,
		DefaultMaxAttempts:   cfg.Tasks.DefaultMaxAttempts,
		DefaultLeaseDuration: cfg.Tasks.DefaultLeaseDuration,
		MaxLeaseDuration:     cfg.Tasks.MaxLeaseDuration,
		RetryPolicy:          tasks.RetryPolicy{BaseDelay: cfg.Tasks.RetryBaseDelay, MaxDelay: cfg.Tasks.RetryMaxDelay},
		OutboxMaxAttempts:    cfg.Tasks.OutboxMaxAttempts,
		OutboxClaimDuration:  cfg.Tasks.OutboxClaimDuration,
	})
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "create task service: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	return cfg, service, cleanup, exitOK
}

func taskCreate(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "-", "JSON request file, or - for stdin")
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	reader, closeReader, err := inputReader(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	defer closeReader()
	request, err := tasks.DecodeCreateRequest(reader)
	if err != nil {
		return taskError(stderr, err)
	}
	value, created, err := service.CreateTask(ctx, request, *actorType, *actorID)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, map[string]any{"created": created, "task": value})
	return exitOK
}

func taskGet(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	jsonOutput, rest, code := parseJSONOnly("task get", args, stderr)
	if code != exitOK || len(rest) != 1 {
		return exitUsage
	}
	id, err := positiveID(rest[0], "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.GetTask(ctx, id)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, jsonOutput, value)
	return exitOK
}

func taskList(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	statusCSV := flags.String("status", "", "comma-separated statuses")
	role := flags.String("role", "", "assigned role")
	unit := flags.String("unit", "", "assigned unit")
	correlation := flags.String("correlation", "", "correlation ID")
	limit := flags.Int("limit", 100, "result limit")
	offset := flags.Int("offset", 0, "result offset")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	statuses, err := parseStatuses(*statusCSV)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	values, err := service.ListTasks(ctx, tasks.TaskFilter{Statuses: statuses, AssignedRoleID: *role, AssignedUnitID: *unit, CorrelationID: *correlation, Limit: *limit, Offset: *offset})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, values)
	return exitOK
}

func taskClaim(ctx context.Context, service *tasks.Service, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task claim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worker := flags.String("worker", "", "worker identifier")
	role := flags.String("role", "", "assigned role filter")
	batch := flags.Int("batch", 1, "claim batch (1-32)")
	lease := flags.Duration("lease", cfg.Tasks.DefaultLeaseDuration, "lease duration")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	values, err := service.ClaimTasks(ctx, tasks.ClaimRequest{WorkerID: *worker, AssignedRoleID: *role, BatchSize: *batch, LeaseDuration: *lease})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, values)
	return exitOK
}

func taskClaimSpecific(ctx context.Context, service *tasks.Service, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task claim-specific", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worker := flags.String("worker", "", "worker identifier")
	role := flags.String("role", "", "assigned role filter")
	lease := flags.Duration("lease", cfg.Tasks.DefaultLeaseDuration, "lease duration")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.ClaimTaskByID(ctx, id, tasks.ClaimRequest{WorkerID: *worker, AssignedRoleID: *role, LeaseDuration: *lease})
	if err != nil {
		return taskError(stderr, err)
	}
	if value.Task.ID != id {
		fmt.Fprintf(stderr, "claim-specific returned task %d, requested task %d (refusing to proceed)\n", value.Task.ID, id)
		return exitDrift
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskStart(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	attempt := flags.Int64("attempt", 0, "attempt ID")
	worker := flags.String("worker", "", "worker identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	token, err := readSecretToken(os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.StartAttempt(ctx, tasks.LeaseCommand{TaskID: id, AttemptID: *attempt, LeaseToken: token, ActorID: *worker})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskHeartbeat(ctx context.Context, service *tasks.Service, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task heartbeat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	attempt := flags.Int64("attempt", 0, "attempt ID")
	worker := flags.String("worker", "", "worker identifier")
	extend := flags.Duration("extend", cfg.Tasks.DefaultLeaseDuration, "lease extension")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	token, err := readSecretToken(os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.Heartbeat(ctx, tasks.LeaseCommand{TaskID: id, AttemptID: *attempt, LeaseToken: token, ActorID: *worker, Extension: *extend})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskResult(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task result", flag.ContinueOnError)
	flags.SetOutput(stderr)
	attempt := flags.Int64("attempt", 0, "attempt ID")
	worker := flags.String("worker", "", "worker identifier")
	resultFile := flags.String("result-file", "", "attempt result JSON file")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 || *resultFile == "" || *resultFile == "-" {
		fmt.Fprintln(stderr, "task result requires --result-file PATH; lease token is read from stdin")
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	reader, closeReader, err := inputReader(*resultFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	result, err := tasks.DecodeAttemptResult(reader)
	closeReader()
	if err != nil {
		return taskError(stderr, err)
	}
	token, err := readSecretToken(os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{LeaseCommand: tasks.LeaseCommand{TaskID: id, AttemptID: *attempt, LeaseToken: token, ActorID: *worker}, Result: result})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskFinalize(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task finalize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outcome := flags.String("outcome", "", "completed|no_action|failed|rejected|cancelled")
	reasonCode := flags.String("reason-code", "", "stable reason code")
	reason := flags.String("reason", "", "human-readable reason")
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.FinalizeTask(ctx, tasks.FinalizeCommand{TaskID: id, Outcome: tasks.FinalOutcome(*outcome), ReasonCode: *reasonCode, Reason: *reason, ActorType: *actorType, ActorID: *actorID})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskBlock(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task block", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reasonCode := flags.String("reason-code", "", "stable reason code")
	reason := flags.String("reason", "", "human-readable reason")
	revoke := flags.Bool("revoke-lease", false, "explicitly revoke an active lease")
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.BlockTask(ctx, tasks.BlockCommand{TaskID: id, ReasonCode: *reasonCode, Reason: *reason, RevokeLease: *revoke, ActorType: *actorType, ActorID: *actorID})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskUnblock(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task unblock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.UnblockTask(ctx, tasks.UnblockCommand{TaskID: id, ActorType: *actorType, ActorID: *actorID})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskCancel(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task cancel", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reasonCode := flags.String("reason-code", "", "stable reason code")
	reason := flags.String("reason", "", "human-readable reason")
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.CancelTask(ctx, tasks.CancelCommand{TaskID: id, ReasonCode: *reasonCode, Reason: *reason, ActorType: *actorType, ActorID: *actorID})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskDependencyAdd(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task dependency add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 2 {
		return exitUsage
	}
	taskID, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	dependencyID, err := positiveID(flags.Arg(1), "dependency")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	if err := service.AddDependency(ctx, tasks.AddDependencyCommand{TaskID: taskID, DependsOnID: dependencyID, ActorType: *actorType, ActorID: *actorID}); err != nil {
		return taskError(stderr, err)
	}
	fmt.Fprintf(stdout, "task %d now depends on task %d\n", taskID, dependencyID)
	return exitOK
}

func taskRequirementAdd(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task requirement add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "-", "requirement JSON file")
	actorType := flags.String("actor-type", "human", "actor type")
	actorID := flags.String("actor-id", "", "actor identifier")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	reader, closeReader, err := inputReader(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	defer closeReader()
	spec, err := tasks.DecodeRequirementSpec(reader)
	if err != nil {
		return taskError(stderr, err)
	}
	value, err := service.AddRequirement(ctx, tasks.AddRequirementCommand{TaskID: id, Spec: spec, ActorType: *actorType, ActorID: *actorID})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskEvidenceAdd(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task evidence add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requirement := flags.Int64("requirement", 0, "requirement ID")
	evidenceType := flags.String("type", "", "artifact|check|approval|condition|result")
	reference := flags.String("reference", "", "opaque evidence reference")
	digest := flags.String("digest", "", "optional lowercase SHA-256")
	recordedBy := flags.String("recorded-by", "", "recorder identifier")
	satisfies := flags.Bool("satisfies", false, "mark the requirement satisfied")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	var requirementID *int64
	if *requirement > 0 {
		requirementID = requirement
	}
	value, err := service.RecordEvidence(ctx, tasks.RecordEvidenceCommand{TaskID: id, RequirementID: requirementID, Type: tasks.RequirementType(*evidenceType), Reference: *reference, Digest: *digest, RecordedBy: *recordedBy, Satisfies: *satisfies})
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskEvents(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task events", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 500, "event limit")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	values, err := service.ListEvents(ctx, id, *limit)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, values)
	return exitOK
}

func taskAttempts(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	jsonOutput, rest, code := parseJSONOnly("task attempts", args, stderr)
	if code != exitOK || len(rest) != 1 {
		return exitUsage
	}
	id, err := positiveID(rest[0], "task")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	values, err := service.ListAttempts(ctx, id)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, jsonOutput, values)
	return exitOK
}

func taskReconcile(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	batch := flags.Int("batch", 100, "reconcile batch")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	value, err := service.Reconcile(ctx, *batch)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func taskDeadList(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("task dead-letter list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 100, "result limit")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	values, err := service.ListDeadLetters(ctx, *limit)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, values)
	return exitOK
}

func taskDeadShow(ctx context.Context, service *tasks.Service, args []string, stdout, stderr io.Writer) int {
	jsonOutput, rest, code := parseJSONOnly("task dead-letter show", args, stderr)
	if code != exitOK || len(rest) != 1 {
		return exitUsage
	}
	id, err := positiveID(rest[0], "dead-letter")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := service.GetDeadLetter(ctx, id)
	if err != nil {
		return taskError(stderr, err)
	}
	writeValue(stdout, jsonOutput, value)
	return exitOK
}

func normalizeTaskSubcommand(args []string) []string {
	if len(args) >= 2 {
		switch args[0] + " " + args[1] {
		case "dependency add":
			return append([]string{"dependency-add"}, args[2:]...)
		case "requirement add":
			return append([]string{"requirement-add"}, args[2:]...)
		case "evidence add":
			return append([]string{"evidence-add"}, args[2:]...)
		case "dead-letter list":
			return append([]string{"dead-list"}, args[2:]...)
		case "dead-letter show":
			return append([]string{"dead-show"}, args[2:]...)
		}
	}
	return args
}

func taskError(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, err)
	switch {
	case errors.Is(err, tasks.ErrDatabaseUnavailable):
		return exitDatabase
	case errors.Is(err, tasks.ErrInvalidInput), errors.Is(err, tasks.ErrForbiddenField):
		return exitUsage
	case errors.Is(err, tasks.ErrConflict), errors.Is(err, tasks.ErrInvalidTransition), errors.Is(err, tasks.ErrIdempotencyConflict),
		errors.Is(err, tasks.ErrDependencyCycle), errors.Is(err, tasks.ErrDependencyUnsatisfied), errors.Is(err, tasks.ErrRequirementsUnsatisfied),
		errors.Is(err, tasks.ErrAssigneeUnavailable), errors.Is(err, tasks.ErrLeaseMismatch), errors.Is(err, tasks.ErrLeaseExpired), errors.Is(err, tasks.ErrActiveLease):
		return exitDrift
	case errors.Is(err, tasks.ErrNotFound):
		return exitInvalid
	default:
		return exitInternal
	}
}

type boolFlag interface {
	IsBoolFlag() bool
}

// parseInterspersed keeps the documented CLI shape usable with the standard
// flag package: positional IDs may appear before or after named options.
func parseInterspersed(set *flag.FlagSet, args []string) error {
	options := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			positionals = append(positionals, args[index+1:]...)
			break
		}
		if argument == "-" || !strings.HasPrefix(argument, "-") {
			positionals = append(positionals, argument)
			continue
		}
		options = append(options, argument)
		nameValue := strings.TrimLeft(argument, "-")
		name, _, hasValue := strings.Cut(nameValue, "=")
		flagDefinition := set.Lookup(name)
		if flagDefinition == nil || hasValue {
			continue
		}
		if value, ok := flagDefinition.Value.(boolFlag); ok && value.IsBoolFlag() {
			continue
		}
		if index+1 >= len(args) {
			continue
		}
		index++
		options = append(options, args[index])
	}
	ordered := append(options, "--")
	ordered = append(ordered, positionals...)
	return set.Parse(ordered)
}

func inputReader(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open %s: %w", path, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func readSecretToken(reader io.Reader) (string, error) {
	body, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil {
		return "", fmt.Errorf("read lease token from stdin: %w", err)
	}
	if len(body) > 4096 {
		return "", errors.New("lease token exceeds 4096 bytes")
	}
	token := strings.TrimSpace(string(body))
	if token == "" || strings.ContainsRune(token, '\x00') || strings.ContainsAny(token, "\r\n\t ") {
		return "", errors.New("stdin must contain exactly one non-empty lease token")
	}
	return token, nil
}

func positiveID(raw, name string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s ID must be a positive integer", name)
	}
	return value, nil
}

func parseStatuses(raw string) ([]tasks.Status, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	values := make([]tasks.Status, 0, len(parts))
	for _, part := range parts {
		status := tasks.Status(strings.TrimSpace(part))
		if !status.Valid() {
			return nil, fmt.Errorf("invalid task status %q", part)
		}
		values = append(values, status)
	}
	return values, nil
}

func printTaskUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl task <command> [options]
commands:
  create --file FILE --actor-id ID
  get ID [--json]
  list [--status CSV] [--role ID] [--unit ID]
  claim --worker ID [--role ID] [--batch N] [--lease DURATION]
  claim-specific TASK_ID --worker ID [--role ID] [--lease DURATION]
  start ID --attempt ID --worker ID          (lease token via stdin)
  heartbeat ID --attempt ID --worker ID      (lease token via stdin)
  result ID --attempt ID --worker ID --result-file FILE (lease token via stdin)
  finalize ID --outcome OUTCOME --actor-id ID
  block ID --reason-code CODE --reason TEXT --actor-id ID [--revoke-lease]
  unblock ID --actor-id ID
  cancel ID --reason-code CODE --reason TEXT --actor-id ID
  dependency add TASK_ID DEPENDENCY_ID
  requirement add TASK_ID --file FILE
  evidence add TASK_ID --type TYPE --reference REF --recorded-by ID
  events ID
  attempts ID
  reconcile [--batch N]
  dead-letter list|show`)
}

func printOutboxUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl outbox <command> [options]
commands:
  claim --consumer ID [--batch N] [--claim-duration DURATION]
  ack EVENT_ID --consumer ID                 (claim token via stdin)
  nack EVENT_ID --consumer ID --error TEXT   (claim token via stdin)
  recover [--batch N]
  status [--json]`)
}
