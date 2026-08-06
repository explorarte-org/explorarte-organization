package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

func openModelEgressRuntime(stderr io.Writer) (*egressbootstrap.Runtime, modelruntime.RuntimeConfig, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return nil, modelruntime.RuntimeConfig{}, func() {}, exitUsage
	}
	runtimeConfig, err := modelruntime.LoadRuntimeConfig(os.LookupEnv, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		fmt.Fprintf(stderr, "load model runtime configuration: %v\n", err)
		return nil, modelruntime.RuntimeConfig{}, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.MigrationTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "model-egress")
	if code != exitOK {
		return nil, modelruntime.RuntimeConfig{}, func() {}, code
	}
	cleanup := func() { store.Close() }
	status, err := runner.Status(ctx)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return nil, modelruntime.RuntimeConfig{}, func() {}, exitInternal
	}
	if !status.Ready {
		cleanup()
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return nil, modelruntime.RuntimeConfig{}, func() {}, exitDrift
	}
	runtime, err := egressbootstrap.Open(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open model egress runtime: %v\n", err)
		return nil, modelruntime.RuntimeConfig{}, func() {}, exitInternal
	}
	return runtime, runtimeConfig, cleanup, exitOK
}

func modelEgress(ctx context.Context, runtime *egressbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
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
			value, err := runtime.Service.Validate(ctx)
			if err != nil {
				return modelEgressError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			return exitOK
		case "diff":
			value, err := runtime.Service.Diff(ctx)
			if err != nil {
				return modelEgressError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			if !value.Synchronized {
				return exitDrift
			}
			return exitOK
		default:
			value, err := runtime.Service.Status(ctx)
			if err != nil {
				return modelEgressError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			if !value.Synchronized {
				return exitDrift
			}
			return exitOK
		}
	case "sync":
		flags := flag.NewFlagSet("model egress sync", flag.ContinueOnError)
		flags.SetOutput(stderr)
		apply := flags.Bool("apply", false, "apply materialization")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Service.Sync(ctx, *apply)
		if err != nil {
			return modelEgressError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		if !*apply && !value.NoOp {
			return exitDrift
		}
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model egress command %q\n", args[0])
		return exitUsage
	}
}

func modelEgressError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "model egress: %v\n", err)
	switch {
	case errors.Is(err, modelegress.ErrInvalidPolicy):
		return exitInvalid
	case errors.Is(err, modelegress.ErrPolicyStale), errors.Is(err, modelegress.ErrPolicyNotFound):
		return exitDrift
	case errors.Is(err, modelegress.ErrPolicyConflict), errors.Is(err, modelegress.ErrEvaluationConflict):
		return exitInvalid
	default:
		return exitInternal
	}
}
