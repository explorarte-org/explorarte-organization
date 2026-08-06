package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
)

func modelPrincipalCommandFromJSON(command struct {
	OrganizationID      string `json:"organization_id"`
	PrincipalKey        string `json:"principal_key"`
	DispatchActorRoleID string `json:"dispatch_actor_role_id"`
	PrincipalKind       string `json:"principal_kind"`
	IdempotencyKey      string `json:"idempotency_key"`
}) modeldispatch.RegisterPrincipalCommand {
	return modeldispatch.RegisterPrincipalCommand{
		OrganizationID: command.OrganizationID, PrincipalKey: command.PrincipalKey,
		DispatchActorRoleID: command.DispatchActorRoleID, PrincipalKind: modeldispatch.PrincipalKind(command.PrincipalKind),
		IdempotencyKey: command.IdempotencyKey,
	}
}

func modelPrincipal(ctx context.Context, runtime *dispatchbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printModelPrincipalUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "register":
		flags := flag.NewFlagSet("model principal register", flag.ContinueOnError)
		flags.SetOutput(stderr)
		path := flags.String("file", "", "JSON command file")
		actorRole := flags.String("actor-role", "", "administrative actor role")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 || strings.TrimSpace(*path) == "" {
			return exitUsage
		}
		body, err := os.ReadFile(*path)
		if err != nil {
			fmt.Fprintf(stderr, "read principal command: %v\n", err)
			return exitInvalid
		}
		var command struct {
			OrganizationID      string `json:"organization_id"`
			PrincipalKey        string `json:"principal_key"`
			DispatchActorRoleID string `json:"dispatch_actor_role_id"`
			PrincipalKind       string `json:"principal_kind"`
			IdempotencyKey      string `json:"idempotency_key"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&command); err != nil {
			fmt.Fprintf(stderr, "decode principal command: %v\n", err)
			return exitInvalid
		}
		var trailing any
		if err = decoder.Decode(&trailing); err != io.EOF {
			fmt.Fprintln(stderr, "principal command contains trailing JSON")
			return exitInvalid
		}
		value, err := runtime.Principals.Register(ctx, *actorRole, modelPrincipalCommandFromJSON(command))
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
		id, err := positiveID(rest[0], "model execution principal")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Principals.Get(ctx, id)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "list":
		flags := flag.NewFlagSet("model principal list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		organizationID := flags.String("organization-id", "", "organization ID")
		limit := flags.Int("limit", 100, "result limit")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Principals.List(ctx, *organizationID, *limit)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "disable":
		flags := flag.NewFlagSet("model principal disable", flag.ContinueOnError)
		flags.SetOutput(stderr)
		actorRole := flags.String("actor-role", "", "administrative actor role")
		reason := flags.String("reason", "", "disable reason code")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "model execution principal")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Principals.Disable(ctx, *actorRole, id, *reason)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model principal command %q\n", args[0])
		return exitUsage
	}
}

func printModelPrincipalUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl model principal <register|get|list|disable> ...")
}
