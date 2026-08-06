package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
)

func runModel(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printModelUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "egress":
		runtime, runtimeConfig, cleanup, code := openModelEgressRuntime(stderr)
		if code != exitOK {
			return code
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), runtimeConfig.CommandTimeout)
		defer cancel()
		return modelEgress(ctx, runtime, args[1:], stdout, stderr)
	case "registry":
		cfg, runtime, cleanup, code := openModelRegistryRuntime(stderr)
		if code != exitOK {
			return code
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), runtime.Config.CommandTimeout)
		defer cancel()
		return modelRegistry(ctx, cfg, runtime, args[1:], stdout, stderr)
	case "invocation":
		_, runtime, cleanup, code := openModelRuntime(stderr)
		if code != exitOK {
			return code
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), runtime.Config.CommandTimeout)
		defer cancel()
		return modelInvocation(ctx, runtime, args[1:], stdout, stderr)
	case "principal":
		_, runtime, cleanup, code := openModelRuntime(stderr)
		if code != exitOK {
			return code
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), runtime.Config.CommandTimeout)
		defer cancel()
		return modelPrincipal(ctx, runtime.Dispatcher, args[1:], stdout, stderr)
	case "assignment":
		_, runtime, cleanup, code := openModelRuntime(stderr)
		if code != exitOK {
			return code
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), runtime.Config.CommandTimeout)
		defer cancel()
		return modelAssignment(ctx, runtime.Dispatcher, args[1:], stdout, stderr)
	case "identity":
		_, runtime, cleanup, code := openModelRuntime(stderr)
		if code != exitOK {
			return code
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), runtime.Config.CommandTimeout)
		defer cancel()
		return modelIdentity(ctx, runtime.Identity, args[1:], stdout, stderr)
	case "worker":
		return runModelWorker(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown model command %q\n", args[0])
		printModelUsage(stderr)
		return exitUsage
	}
}

func openModelRegistryRuntime(stderr io.Writer) (config.Config, *modelbootstrap.RegistryRuntime, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.MigrationTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "model-registry")
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
	runtime, err := modelbootstrap.OpenRegistry(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open model registry runtime: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	return cfg, runtime, cleanup, exitOK
}

func openModelRuntime(stderr io.Writer) (config.Config, *modelbootstrap.Runtime, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.MigrationTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "model-runtime")
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
	runtime, err := modelbootstrap.Open(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open model runtime: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	return cfg, runtime, cleanup, exitOK
}

func modelRegistry(ctx context.Context, cfg config.Config, runtime *modelbootstrap.RegistryRuntime, args []string, stdout, stderr io.Writer) int {
	_ = cfg
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case "validate", "diff", "status":
		jsonOutput, rest, code := parseModelJSON(args[1:], stderr)
		if code != exitOK || len(rest) != 0 {
			return exitUsage
		}
		switch args[0] {
		case "validate":
			value, err := runtime.Registry.Validate(ctx)
			if err != nil {
				return modelError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			return exitOK
		case "diff":
			value, err := runtime.Registry.Diff(ctx)
			if err != nil {
				return modelError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			if !value.Synchronized {
				return exitDrift
			}
			return exitOK
		default:
			value, err := runtime.Registry.Status(ctx)
			if err != nil {
				return modelError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			if !value.Synchronized {
				return exitDrift
			}
			return exitOK
		}
	case "sync":
		flags := flag.NewFlagSet("model registry sync", flag.ContinueOnError)
		flags.SetOutput(stderr)
		apply := flags.Bool("apply", false, "apply materialization")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Registry.Sync(ctx, *apply, runtime.Config.OutboxMaxAttempts)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		if !*apply && !value.NoOp {
			return exitDrift
		}
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model registry command %q\n", args[0])
		return exitUsage
	}
}

func modelInvocation(ctx context.Context, runtime *modelbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("model invocation create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		path := flags.String("file", "", "JSON command file")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 || strings.TrimSpace(*path) == "" {
			return exitUsage
		}
		body, err := os.ReadFile(*path)
		if err != nil {
			fmt.Fprintf(stderr, "read invocation command: %v\n", err)
			return exitInvalid
		}
		var command modelruntime.CreateInvocationCommand
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&command); err != nil {
			fmt.Fprintf(stderr, "decode invocation command: %v\n", err)
			return exitInvalid
		}
		var trailing any
		if err = decoder.Decode(&trailing); err != io.EOF {
			fmt.Fprintln(stderr, "invocation command contains trailing JSON")
			return exitInvalid
		}
		value, err := runtime.Invocations.Create(ctx, command)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "get":
		jsonOutput, rest, code := parseModelJSON(args[1:], stderr)
		if code != exitOK || len(rest) != 1 {
			return exitUsage
		}
		id, err := positiveID(rest[0], "model invocation")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Invocations.Get(ctx, id)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "list":
		flags := flag.NewFlagSet("model invocation list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		limit := flags.Int("limit", 100, "result limit")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Invocations.List(ctx, *limit)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "dispatch":
		// Branch 10: the dispatching identity is resolved from
		// ORG_MODEL_EXECUTION_PRINCIPAL_KEY, not a CLI-supplied claimant. No flag
		// may select who is dispatching or which principal/assignment to use.
		for _, forbidden := range []string{"--claimed-by", "--principal", "--assignment"} {
			for _, arg := range args[1:] {
				if arg == forbidden || strings.HasPrefix(arg, forbidden+"=") {
					fmt.Fprintf(stderr, "unsupported flag %q: dispatcher identity is resolved from ORG_MODEL_EXECUTION_PRINCIPAL_KEY\n", forbidden)
					return exitUsage
				}
			}
		}
		flags := flag.NewFlagSet("model invocation dispatch", flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "model invocation")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Dispatch.Dispatch(ctx, id)
		if err != nil {
			if value.Invocation.ID > 0 {
				writeValue(stdout, *jsonOutput, value)
			}
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "cancel":
		flags := flag.NewFlagSet("model invocation cancel", flag.ContinueOnError)
		flags.SetOutput(stderr)
		actor := flags.String("actor-role", "", "requesting role")
		reason := flags.String("reason", "", "cancellation reason")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "model invocation")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Invocations.Cancel(ctx, id, *actor, *reason)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "reconcile":
		flags := flag.NewFlagSet("model invocation reconcile", flag.ContinueOnError)
		flags.SetOutput(stderr)
		batch := flags.Int("batch", runtime.Config.ReconcileBatchSize, "batch size")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Invocations.Reconcile(ctx, *batch)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model invocation command %q\n", args[0])
		return exitUsage
	}
}

func parseModelJSON(args []string, stderr io.Writer) (bool, []string, int) {
	result := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--json" {
			result = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			fmt.Fprintf(stderr, "unknown option %q\n", arg)
			return false, nil, exitUsage
		}
		rest = append(rest, arg)
	}
	return result, rest, exitOK
}
func modelError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "model runtime: %v\n", err)
	switch {
	case errors.Is(err, modelruntime.ErrContextRejected):
		return exitContextRejected
	case errors.Is(err, modelruntime.ErrDisabled), errors.Is(err, modelruntime.ErrExecutionIdentityDenied), errors.Is(err, modelidentity.ErrAuthorizationDenied), errors.Is(err, modelidentity.ErrKeyInactive), errors.Is(err, modelidentity.ErrReplayDenied), errors.Is(err, modelruntime.ErrAuthorizationDenied), errors.Is(err, modelruntime.ErrProviderUnavailable), errors.Is(err, modelruntime.ErrExecutionPrincipalDisabled), errors.Is(err, modelruntime.ErrExecutionPrincipalMismatch), errors.Is(err, modeldispatch.ErrAuthorizationDenied), errors.Is(err, modeldispatch.ErrPrincipalDisabled), errors.Is(err, modeldispatch.ErrPrincipalMismatch):
		return exitDenied
	case errors.Is(err, modelruntime.ErrInvalidRequest), errors.Is(err, modelruntime.ErrExecutionIdentityUnpinned), errors.Is(err, modelidentity.ErrInvalidPolicy), errors.Is(err, modelidentity.ErrPolicyNotFound), errors.Is(err, modelidentity.ErrPolicyConflict), errors.Is(err, modelidentity.ErrPolicyStale), errors.Is(err, modelidentity.ErrInvalidKey), errors.Is(err, modelidentity.ErrKeyNotFound), errors.Is(err, modelidentity.ErrKeyConflict), errors.Is(err, modelidentity.ErrChallengeNotFound), errors.Is(err, modelidentity.ErrChallengeExpired), errors.Is(err, modelidentity.ErrChallengeConsumed), errors.Is(err, modelidentity.ErrAssertionInvalid), errors.Is(err, modelruntime.ErrTaskAttemptRejected), errors.Is(err, modelruntime.ErrCapabilityMismatch), errors.Is(err, modelruntime.ErrNotFound), errors.Is(err, modelruntime.ErrBindingNotFound), errors.Is(err, modelruntime.ErrConflict), errors.Is(err, modelruntime.ErrClaimUnavailable), errors.Is(err, modelruntime.ErrConcurrencyLimit), errors.Is(err, modelruntime.ErrClaimTokenMismatch), errors.Is(err, modelruntime.ErrResponseRejected), errors.Is(err, modelruntime.ErrCancellationRequested), errors.Is(err, modelruntime.ErrAssignmentUnavailable), errors.Is(err, modelruntime.ErrAssignmentRevisionDrift), errors.Is(err, modelruntime.ErrAssignmentVigencyExpired), errors.Is(err, modelruntime.ErrAssignmentQuotaExhausted), errors.Is(err, modelruntime.ErrDispatcherAssignmentUnpinned), errors.Is(err, modeldispatch.ErrInvalidRequest), errors.Is(err, modeldispatch.ErrNotFound), errors.Is(err, modeldispatch.ErrConflict), errors.Is(err, modeldispatch.ErrRoleNotEligible), errors.Is(err, modeldispatch.ErrAssignmentNotFound), errors.Is(err, modeldispatch.ErrAssignmentInactive), errors.Is(err, modeldispatch.ErrAssignmentExpired), errors.Is(err, modeldispatch.ErrAssignmentExhausted), errors.Is(err, modeldispatch.ErrAssignmentRevoked), errors.Is(err, modeldispatch.ErrAssignmentConflict), errors.Is(err, modeldispatch.ErrRevisionDrift), errors.Is(err, modeldispatch.ErrTaskAttemptRejected):
		return exitInvalid
	case errors.Is(err, modelruntime.ErrDatabaseUnavailable), errors.Is(err, modeldispatch.ErrDatabaseUnavailable):
		return exitDatabase
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, modelruntime.ErrAmbiguousOutcome):
		return exitInternal
	default:
		return exitInternal
	}
}
func printModelUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl model <registry|egress|invocation|principal|assignment|identity|worker> ...")
	fmt.Fprintln(out, "  orgctl model registry <validate|diff|sync|status> [--json] [--apply]")
	fmt.Fprintln(out, "  orgctl model egress <validate|diff|sync|status> [--json] [--apply]")
	fmt.Fprintln(out, "  orgctl model invocation <create|get|list|dispatch|cancel|reconcile> ...")
	fmt.Fprintln(out, "  orgctl model principal <register|get|list|disable> ...")
	fmt.Fprintln(out, "  orgctl model assignment <create|get|list|revoke|expire> ...")
	fmt.Fprintln(out, "  orgctl model identity <policy|key> ...")
	fmt.Fprintln(out, "  orgctl model worker run")
}
