package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

func runContext(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printContextUsage(stderr)
		return exitUsage
	}
	if args[0] == "validate-source" {
		return contextValidateSource(args[1:], stdout, stderr)
	}
	cfg, runtime, cleanup, code := openContextRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Context.CommandTimeout)
	defer cancel()
	switch args[0] {
	case "build":
		return contextBuild(ctx, cfg, runtime, args[1:], stdout, stderr)
	case "get":
		return contextGet(ctx, runtime, args[1:], stdout, stderr)
	case "list":
		return contextList(ctx, cfg, runtime, args[1:], stdout, stderr)
	case "render":
		return contextRender(ctx, runtime, args[1:], stdout, stderr)
	case "validate":
		return contextValidate(ctx, runtime, args[1:], stdout, stderr)
	case "invalidate":
		return contextInvalidate(ctx, runtime, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown context command %q\n", args[0])
		printContextUsage(stderr)
		return exitUsage
	}
}

func openContextRuntime(stderr io.Writer) (config.Config, *contextbootstrap.Runtime, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Context.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "context")
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
	runtime, err := contextbootstrap.Open(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open context runtime: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	return cfg, runtime, cleanup, exitOK
}

func contextValidateSource(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("context validate-source", flag.ContinueOnError)
	flags.SetOutput(stderr)
	kind := flags.String("kind", "", "profile, agent, or skill")
	path := flags.String("path", "", "relative source path")
	expectedRole := flags.String("expected-role", "", "expected canonical role")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	cfg, runtime, cleanup, code := openContextRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Context.CommandTimeout)
	defer cancel()
	doc, err := runtime.Documents.Load(ctx, *path, int64(cfg.Context.MaxSegmentBytes))
	if err != nil {
		return contextError(stderr, err)
	}
	result := contextengine.SourceValidation{Valid: true, Path: *path, ContentHash: doc.Hash}
	switch *kind {
	case "agent":
		result.Kind = contextengine.SourceOrganizationAgent
		err = contextengine.ValidateAgent(doc)
	case "profile":
		result.Kind = contextengine.SourceRoleProfile
		if *expectedRole == "" {
			fmt.Fprintln(stderr, "--expected-role is required for profile")
			return exitUsage
		}
		role, getErr := runtime.Registry.GetRole(ctx, runtime.OrganizationID, *expectedRole)
		if getErr != nil {
			err = getErr
			break
		}
		memory := ""
		if role.MemoryDomain != nil {
			memory = *role.MemoryDomain
		}
		err = contextengine.ValidateProfile(doc, role.UnitID, role.RoleSlug, memory)
	case "skill":
		result.Kind = contextengine.SourceApprovedSkill
		if *expectedRole == "" {
			fmt.Fprintln(stderr, "--expected-role is required for skill")
			return exitUsage
		}
		role, getErr := runtime.Registry.GetRole(ctx, runtime.OrganizationID, *expectedRole)
		if getErr != nil {
			err = getErr
			break
		}
		name, _ := doc.Frontmatter["name"].(string)
		record, ok := runtime.Skills.Record(strings.TrimSpace(name))
		if !ok {
			memory := ""
			if role.MemoryDomain != nil {
				memory = *role.MemoryDomain
			}
			record = contextengine.SkillRecord{ID: strings.TrimSpace(name), RoleID: role.ID, Department: role.UnitID, MemoryDomain: memory, Path: *path}
		}
		result.Warnings, err = contextengine.ValidateSkill(doc, record)
	default:
		fmt.Fprintln(stderr, "--kind must be profile, agent, or skill")
		return exitUsage
	}
	if err != nil {
		result.Valid = false
		writeValue(stdout, *jsonOutput, result)
		return contextError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, result)
	return exitOK
}

func contextBuild(ctx context.Context, cfg config.Config, runtime *contextbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("context build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "actor role")
	purpose := flags.String("purpose", "", "context purpose")
	idempotency := flags.String("idempotency-key", "", "idempotency key")
	project := flags.String("project-ref", "", "project reference")
	task := flags.String("task-ref", "", "task reference")
	correlation := flags.String("correlation-id", "", "correlation ID")
	causation := flags.String("causation-id", "", "causation ID")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	var skills stringListFlag
	flags.Var(&skills, "skill", "active assigned skill ID (repeatable)")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	revision, err := runtime.Registry.GetCurrentRevision(ctx, runtime.OrganizationID)
	if err != nil || revision == nil {
		if err == nil {
			err = registry.ErrNotFound
		}
		return contextError(stderr, err)
	}
	result, err := runtime.Service.Build(ctx, contextengine.BuildRequest{OrganizationID: runtime.OrganizationID, OrganizationRevisionID: revision.ID, ActorRoleID: *actor, Purpose: *purpose, ProjectRef: *project, TaskRef: *task, RequestedSkillIDs: skills, IdempotencyKey: *idempotency, CorrelationID: *correlation, CausationID: *causation})
	if err != nil {
		return contextError(stderr, err)
	}
	result.Snapshot.Segments = redactedSegments(result.Snapshot.Segments)
	writeValue(stdout, *jsonOutput, result)
	return exitOK
}

func contextGet(ctx context.Context, runtime *contextbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("context get", flag.ContinueOnError)
	flags.SetOutput(stderr)
	include := flags.Bool("include-segments", false, "include segment metadata and content")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "context snapshot")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := runtime.Service.Get(ctx, id, *include)
	if err != nil {
		return contextError(stderr, err)
	}
	if !*include {
		value.Segments = nil
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func contextList(ctx context.Context, cfg config.Config, runtime *contextbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("context list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "actor role")
	status := flags.String("status", "", "ready or invalidated")
	limit := flags.Int("limit", 100, "result limit")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	if *status != "" && *status != "ready" && *status != "invalidated" {
		return exitUsage
	}
	values, err := runtime.Service.List(ctx, contextengine.ListFilter{OrganizationID: runtime.OrganizationID, ActorRoleID: *actor, Status: contextengine.SnapshotStatus(*status), Limit: *limit})
	if err != nil {
		return contextError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, values)
	_ = cfg
	return exitOK
}

func contextRender(ctx context.Context, runtime *contextbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("context render", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "-", "output path or -")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "context snapshot")
	if err != nil {
		return exitUsage
	}
	body, err := runtime.Service.Render(ctx, id)
	if err != nil {
		return contextError(stderr, err)
	}
	if *output == "-" {
		if _, err = stdout.Write(body); err == nil {
			_, err = fmt.Fprintln(stdout)
		}
		if err != nil {
			return exitInternal
		}
		return exitOK
	}
	clean := filepath.Clean(*output)
	if clean == "." || strings.ContainsRune(clean, 0) {
		return exitUsage
	}
	if err = os.WriteFile(clean, body, 0o600); err != nil {
		fmt.Fprintf(stderr, "write rendered context: %v\n", err)
		return exitInternal
	}
	return exitOK
}

func contextValidate(ctx context.Context, runtime *contextbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	jsonOutput, rest, code := parseJSONOnly("context validate", args, stderr)
	if code != exitOK || len(rest) != 1 {
		return exitUsage
	}
	id, err := positiveID(rest[0], "context snapshot")
	if err != nil {
		return exitUsage
	}
	value, err := runtime.Service.Validate(ctx, id)
	if err != nil {
		return contextError(stderr, err)
	}
	writeValue(stdout, jsonOutput, value)
	if !value.Valid {
		return exitContextStale
	}
	return exitOK
}

func contextInvalidate(ctx context.Context, runtime *contextbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("context invalidate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "requester or owner role")
	reason := flags.String("reason", "", "invalidation reason")
	correlation := flags.String("correlation-id", "", "correlation ID")
	causation := flags.String("causation-id", "", "causation ID")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "context snapshot")
	if err != nil {
		return exitUsage
	}
	value, err := runtime.Service.Invalidate(ctx, contextengine.InvalidateCommand{SnapshotID: id, ActorRoleID: *actor, Reason: *reason, CorrelationID: *correlation, CausationID: *causation})
	if err != nil {
		return contextError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func contextError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "context: %v\n", err)
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return exitInternal
	case errors.Is(err, contextengine.ErrSnapshotInvalidated), errors.Is(err, contextengine.ErrSnapshotStale):
		return exitContextStale
	case errors.Is(err, contextengine.ErrRejected), errors.Is(err, contextengine.ErrInvalidRequest), errors.Is(err, contextengine.ErrIdempotencyConflict):
		return exitContextRejected
	case errors.Is(err, contextengine.ErrSnapshotNotFound):
		return exitInvalid
	case errors.Is(err, contextengine.ErrDatabaseUnavailable):
		return exitDatabase
	default:
		return exitInternal
	}
}

func redactedSegments(values []contextengine.Segment) []contextengine.Segment {
	result := make([]contextengine.Segment, len(values))
	copy(result, values)
	for i := range result {
		result[i].Content = nil
	}
	return result
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }
func (s *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("skill ID cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

func printContextUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl context <validate-source|build|get|list|render|validate|invalidate> [options]")
}
