package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
)

func runStaging(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printStagingUsage(stderr)
		return exitUsage
	}
	cfg, runtime, cleanup, code := openStagingRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Staging.CommandTimeout)
	defer cancel()
	switch args[0] {
	case "repo":
		return stagingRepo(ctx, runtime, args[1:], stdout, stderr)
	case "workspace":
		return stagingWorkspace(ctx, runtime, args[1:], stdout, stderr)
	case "check":
		return stagingCheck(ctx, runtime, args[1:], stdout, stderr)
	case "promotion":
		return stagingPromotion(ctx, runtime, args[1:], stdout, stderr)
	case "reconcile":
		flags := flag.NewFlagSet("staging reconcile", flag.ContinueOnError)
		flags.SetOutput(stderr)
		batch := flags.Int("batch", cfg.Staging.ReconcileBatchSize, "batch size (1-500)")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		result, err := runtime.Service.Reconcile(ctx, *batch)
		if err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, result)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown staging command %q\n", args[0])
		printStagingUsage(stderr)
		return exitUsage
	}
}

func stagingRepo(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case "list":
		jsonOutput, rest, code := parseJSONOnly("staging repo list", args[1:], stderr)
		if code != exitOK || len(rest) != 0 {
			return exitUsage
		}
		writeValue(stdout, jsonOutput, runtime.Catalog.List(ctx))
		return exitOK
	case "get":
		jsonOutput, rest, code := parseJSONOnly("staging repo get", args[1:], stderr)
		if code != exitOK || len(rest) != 1 {
			return exitUsage
		}
		repo, hash, err := runtime.Catalog.Get(ctx, rest[0])
		if err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, jsonOutput, staging.RepositoryView{ID: repo.ID, Enabled: repo.Enabled, AllowedTargetRefs: repo.AllowedTargetRefs, ConfigHash: hash})
		return exitOK
	case "validate":
		jsonOutput, rest, code := parseJSONOnly("staging repo validate", args[1:], stderr)
		if code != exitOK || len(rest) != 1 {
			return exitUsage
		}
		if err := runtime.Catalog.Validate(ctx, rest[0]); err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"repository_id": rest[0], "valid": true})
		return exitOK
	default:
		return exitUsage
	}
}

func stagingWorkspace(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("staging workspace create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		task := flags.Int64("task", 0, "task ID")
		attempt := flags.Int64("attempt", 0, "attempt ID")
		repo := flags.String("repository", "", "repository ID")
		base := flags.String("base", "", "base commit")
		target := flags.String("target-ref", "", "target ref")
		holder := flags.String("holder", "", "lease holder")
		actor := flags.String("actor-role", "", "actor role")
		requirement := flags.Int64("artifact-requirement", 0, "artifact requirement ID")
		tokenStdin := flags.Bool("lease-token-stdin", false, "read lease token from stdin")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 || !*tokenStdin {
			return exitUsage
		}
		token, err := readStagingToken()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		value, err := runtime.Service.CreateWorkspace(ctx, staging.CreateWorkspaceCommand{TaskID: *task, AttemptID: *attempt, RepositoryID: *repo, BaseCommit: *base, TargetRef: *target, HolderID: *holder, ActorRoleID: *actor, ArtifactRequirementID: *requirement, LeaseToken: token})
		token = ""
		if err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "get", "inspect", "seal", "abandon", "cleanup":
		flags := flag.NewFlagSet("staging workspace "+args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		holder := flags.String("holder", "", "lease holder")
		actor := flags.String("actor-role", "", "actor role")
		reasonCode := flags.String("reason-code", "", "reason code")
		reason := flags.String("reason", "", "reason")
		tokenStdin := flags.Bool("lease-token-stdin", false, "read lease token from stdin")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "workspace")
		if err != nil {
			return exitUsage
		}
		switch args[0] {
		case "get":
			value, err := runtime.Service.GetWorkspace(ctx, id)
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "inspect":
			value, err := runtime.Service.InspectWorkspace(ctx, id)
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "seal":
			if !*tokenStdin {
				return exitUsage
			}
			token, err := readStagingToken()
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitUsage
			}
			value, err := runtime.Service.SealWorkspace(ctx, staging.SealWorkspaceCommand{WorkspaceID: id, HolderID: *holder, ActorRoleID: *actor, LeaseToken: token})
			token = ""
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "abandon":
			value, err := runtime.Service.AbandonWorkspace(ctx, staging.AbandonWorkspaceCommand{WorkspaceID: id, ActorRoleID: *actor, ReasonCode: *reasonCode, Reason: *reason})
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "cleanup":
			value, err := runtime.Service.CleanupWorkspace(ctx, staging.CleanupWorkspaceCommand{WorkspaceID: id, ActorRoleID: *actor})
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		}
	case "list":
		flags := flag.NewFlagSet("staging workspace list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		status := flags.String("status", "", "workspace status")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		filter := staging.WorkspaceFilter{Status: staging.WorkspaceStatus(*status), Limit: 100}
		if *status != "" && !filter.Status.Valid() {
			return exitUsage
		}
		values, err := runtime.Service.ListWorkspaces(ctx, filter)
		if err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, values)
		return exitOK
	}
	return exitUsage
}

func stagingCheck(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "record" {
		return exitUsage
	}
	flags := flag.NewFlagSet("staging check record", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspace := flags.Int64("workspace", 0, "workspace ID")
	requirement := flags.Int64("requirement", 0, "requirement ID")
	name := flags.String("name", "", "check name")
	status := flags.String("status", "", "passed or failed")
	reference := flags.String("reference", "", "opaque evidence reference")
	digest := flags.String("digest", "", "optional SHA-256")
	actor := flags.String("actor-role", "", "actor role")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	value, err := runtime.Service.RecordCheck(ctx, staging.RecordCheckCommand{WorkspaceID: *workspace, RequirementID: *requirement, Name: *name, Status: staging.CheckStatus(*status), Reference: *reference, Digest: *digest, ActorRoleID: *actor})
	if err != nil {
		return stagingError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func stagingPromotion(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case "request":
		flags := flag.NewFlagSet("staging promotion request", flag.ContinueOnError)
		flags.SetOutput(stderr)
		workspace := flags.Int64("workspace", 0, "workspace ID")
		actor := flags.String("actor-role", "", "actor role")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Service.RequestPromotion(ctx, staging.RequestPromotionCommand{WorkspaceID: *workspace, ActorRoleID: *actor})
		if err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "get", "apply", "cancel", "review":
		flags := flag.NewFlagSet("staging promotion "+args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		requirement := flags.Int64("requirement", 0, "approval requirement ID")
		decision := flags.String("decision", "", "approve or reject")
		actor := flags.String("actor-role", "", "actor role")
		reasonCode := flags.String("reason-code", "", "reason code")
		reason := flags.String("reason", "", "reason")
		reference := flags.String("reference", "", "opaque reference")
		digest := flags.String("digest", "", "optional digest")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "promotion")
		if err != nil {
			return exitUsage
		}
		switch args[0] {
		case "get":
			value, err := runtime.Service.GetPromotion(ctx, id)
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "apply":
			value, err := runtime.Service.ApplyPromotion(ctx, staging.ApplyPromotionCommand{PromotionID: id, ActorRoleID: *actor})
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "cancel":
			value, err := runtime.Service.CancelPromotion(ctx, staging.CancelPromotionCommand{PromotionID: id, ActorRoleID: *actor, ReasonCode: *reasonCode, Reason: *reason})
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		case "review":
			value, err := runtime.Service.SubmitReview(ctx, staging.SubmitReviewCommand{PromotionID: id, RequirementID: *requirement, Decision: staging.ReviewDecision(*decision), ActorRoleID: *actor, Reason: *reason, Reference: *reference, Digest: *digest})
			if err != nil {
				return stagingError(stderr, err)
			}
			writeValue(stdout, *jsonOutput, value)
			return exitOK
		}
	case "list":
		flags := flag.NewFlagSet("staging promotion list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		status := flags.String("status", "", "promotion status")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		filter := staging.PromotionFilter{Status: staging.PromotionStatus(*status), Limit: 100}
		if *status != "" && !filter.Status.Valid() {
			return exitUsage
		}
		values, err := runtime.Service.ListPromotions(ctx, filter)
		if err != nil {
			return stagingError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, values)
		return exitOK
	}
	return exitUsage
}

func openStagingRuntime(stderr io.Writer) (config.Config, *bootstrap.Runtime, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Staging.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "staging")
	if code != exitOK {
		return config.Config{}, nil, func() {}, code
	}
	cleanup := func() { store.Close() }
	status, err := runner.Status(ctx)
	if err != nil {
		cleanup()
		return config.Config{}, nil, func() {}, exitInternal
	}
	if !status.Ready {
		cleanup()
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return config.Config{}, nil, func() {}, exitDrift
	}
	runtime, err := bootstrap.Open(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open staging runtime: %v\n", err)
		return config.Config{}, nil, func() {}, exitInvalid
	}
	return cfg, runtime, cleanup, exitOK
}

func readStagingToken() (string, error) {
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("lease token must be piped through stdin")
	}
	return readSecretToken(os.Stdin)
}
func stagingError(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, redactStagingError(err))
	switch {
	case errors.Is(err, staging.ErrDatabaseUnavailable):
		return exitDatabase
	case errors.Is(err, staging.ErrInvalidInput):
		return exitUsage
	case errors.Is(err, staging.ErrWorkspaceNotFound):
		return exitInvalid
	case errors.Is(err, staging.ErrLeaseRequired):
		return exitUsage
	case errors.Is(err, staging.ErrWorkspaceExists),
		errors.Is(err, staging.ErrWorkspaceNotActive),
		errors.Is(err, staging.ErrWorkspaceSealed),
		errors.Is(err, staging.ErrWorkspaceDirty),
		errors.Is(err, staging.ErrWorkspaceMissing),
		errors.Is(err, staging.ErrRepositoryDenied),
		errors.Is(err, staging.ErrTargetRefDenied),
		errors.Is(err, staging.ErrConflict),
		errors.Is(err, staging.ErrInvalidTransition),
		errors.Is(err, staging.ErrCapabilityDenied),
		errors.Is(err, staging.ErrPolicyRevisionMismatch),
		errors.Is(err, staging.ErrLeaseInvalid),
		errors.Is(err, staging.ErrGitConflict),
		errors.Is(err, staging.ErrNoChanges),
		errors.Is(err, staging.ErrArtifactCorrupt),
		errors.Is(err, staging.ErrPromotionGatesUnsatisfied),
		errors.Is(err, staging.ErrTargetMoved),
		errors.Is(err, staging.ErrUnsafeRepository):
		return exitDrift
	default:
		return exitInternal
	}
}
func redactStagingError(err error) string {
	value := err.Error()
	lower := strings.ToLower(value)
	for _, marker := range []string{"lease_token", "lease-token", "token_hash", "token-hash", "claim_token", "claim-token"} {
		if strings.Contains(lower, marker) {
			return "staging operation failed: sensitive details redacted"
		}
	}
	if len(value) > 2000 {
		value = value[:2000]
	}
	return value
}
func printStagingUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl staging <repo|workspace|check|promotion|reconcile> ...")
}
