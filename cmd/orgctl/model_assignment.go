package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
)

func modelAssignment(ctx context.Context, runtime *dispatchbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printModelAssignmentUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("model assignment create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		organizationID := flags.String("organization-id", "", "organization ID")
		taskID := flags.Int64("task-id", 0, "task ID")
		attemptID := flags.Int64("attempt-id", 0, "task attempt ID")
		subjectRole := flags.String("subject-role", "", "subject role ID")
		principalKey := flags.String("principal-key", "", "execution principal key")
		maxInvocations := flags.Int("max-invocations", 1, "maximum invocations authorized")
		validUntilRaw := flags.String("valid-until", "", "RFC3339 vigency end (mutually exclusive with --ttl)")
		ttlRaw := flags.String("ttl", "", "vigency duration from now (mutually exclusive with --valid-until)")
		idempotencyKey := flags.String("idempotency-key", "", "idempotency key")
		actorRole := flags.String("actor-role", "", "administrative actor role")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		command := modeldispatch.CreateAssignmentCommand{
			OrganizationID: *organizationID, TaskID: *taskID, AttemptID: *attemptID,
			SubjectRoleID: *subjectRole, ExecutionPrincipalKey: *principalKey,
			MaxInvocations: *maxInvocations, IdempotencyKey: *idempotencyKey,
		}
		if *validUntilRaw != "" {
			parsed, err := time.Parse(time.RFC3339, *validUntilRaw)
			if err != nil {
				fmt.Fprintf(stderr, "invalid --valid-until: %v\n", err)
				return exitUsage
			}
			command.ValidUntil = &parsed
		}
		if *ttlRaw != "" {
			parsed, err := time.ParseDuration(*ttlRaw)
			if err != nil {
				fmt.Fprintf(stderr, "invalid --ttl: %v\n", err)
				return exitUsage
			}
			command.TTL = &parsed
		}
		value, err := runtime.Assignments.Create(ctx, *actorRole, command)
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
		id, err := positiveID(rest[0], "model dispatcher assignment")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Assignments.Get(ctx, id)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "list":
		flags := flag.NewFlagSet("model assignment list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		organizationID := flags.String("organization-id", "", "organization ID")
		limit := flags.Int("limit", 100, "result limit")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Assignments.List(ctx, *organizationID, *limit)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "revoke":
		flags := flag.NewFlagSet("model assignment revoke", flag.ContinueOnError)
		flags.SetOutput(stderr)
		actorRole := flags.String("actor-role", "", "administrative actor role")
		reason := flags.String("reason", "", "revocation reason code")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "model dispatcher assignment")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Assignments.Revoke(ctx, *actorRole, id, *reason)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "expire":
		flags := flag.NewFlagSet("model assignment expire", flag.ContinueOnError)
		flags.SetOutput(stderr)
		organizationID := flags.String("organization-id", "", "organization ID")
		batch := flags.Int("batch", runtime.Config.AssignmentExpireBatchSize, "batch size")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Assignments.Expire(ctx, *organizationID, *batch)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model assignment command %q\n", args[0])
		return exitUsage
	}
}

func printModelAssignmentUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl model assignment <create|get|list|revoke|expire> ...")
}
