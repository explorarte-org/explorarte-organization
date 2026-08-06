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
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitybootstrap "github.com/Mireuz13/explorarte-organization/internal/modelidentity/bootstrap"
)

type identityKeyCommandFile struct {
	OrganizationID        string     `json:"organization_id"`
	ExecutionPrincipalKey string     `json:"execution_principal_key"`
	PublicKeyBase64       string     `json:"public_key_base64"`
	SecretRef             string     `json:"secret_ref"`
	ValidUntil            *time.Time `json:"valid_until,omitempty"`
	IdempotencyKey        string     `json:"idempotency_key"`
}

func decodeIdentityKeyCommand(path string) (modelidentity.RegisterKeyCommand, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return modelidentity.RegisterKeyCommand{}, err
	}
	var input identityKeyCommandFile
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return modelidentity.RegisterKeyCommand{}, err
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return modelidentity.RegisterKeyCommand{}, fmt.Errorf("identity key command contains trailing JSON")
	}
	return modelidentity.RegisterKeyCommand{
		OrganizationID: input.OrganizationID, ExecutionPrincipalKey: input.ExecutionPrincipalKey,
		PublicKeyBase64: input.PublicKeyBase64, SecretRef: input.SecretRef,
		ValidUntil: input.ValidUntil, IdempotencyKey: input.IdempotencyKey,
	}, nil
}

func modelIdentity(ctx context.Context, runtime *identitybootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if runtime == nil || len(args) == 0 {
		printModelIdentityUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "policy":
		return modelIdentityPolicy(ctx, runtime, args[1:], stdout, stderr)
	case "key":
		return modelIdentityKey(ctx, runtime, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown model identity command %q\n", args[0])
		printModelIdentityUsage(stderr)
		return exitUsage
	}
}

func modelIdentityPolicy(ctx context.Context, runtime *identitybootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
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
			value, err := runtime.Policy.Validate(ctx)
			if err != nil {
				return modelError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			return exitOK
		case "diff":
			value, err := runtime.Policy.Diff(ctx)
			if err != nil {
				return modelError(stderr, err)
			}
			writeValue(stdout, jsonOutput, value)
			if !value.Synchronized {
				return exitDrift
			}
			return exitOK
		default:
			value, err := runtime.Policy.Status(ctx)
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
		flags := flag.NewFlagSet("model identity policy sync", flag.ContinueOnError)
		flags.SetOutput(stderr)
		apply := flags.Bool("apply", false, "apply policy materialization")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Policy.Sync(ctx, *apply)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		if !*apply && !value.NoOp {
			return exitDrift
		}
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model identity policy command %q\n", args[0])
		return exitUsage
	}
}

func modelIdentityKey(ctx context.Context, runtime *identitybootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitUsage
	}
	switch args[0] {
	case "register", "rotate":
		flags := flag.NewFlagSet("model identity key "+args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		path := flags.String("file", "", "JSON command file containing a public key and opaque secret reference")
		actorRole := flags.String("actor-role", "", "administrative actor role")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 || strings.TrimSpace(*path) == "" || strings.TrimSpace(*actorRole) == "" {
			return exitUsage
		}
		command, err := decodeIdentityKeyCommand(*path)
		if err != nil {
			fmt.Fprintf(stderr, "decode identity key command: %v\n", err)
			return exitInvalid
		}
		var value modelidentity.RegisterKeyResult
		if args[0] == "register" {
			value, err = runtime.Keys.Register(ctx, *actorRole, command)
		} else {
			value, err = runtime.Keys.Rotate(ctx, *actorRole, command)
		}
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
		id, err := positiveID(rest[0], "model execution identity key")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Keys.Get(ctx, id)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "list":
		flags := flag.NewFlagSet("model identity key list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		principalID := flags.Int64("principal-id", 0, "optional execution principal ID")
		limit := flags.Int("limit", 100, "result limit")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 0 {
			return exitUsage
		}
		value, err := runtime.Keys.List(ctx, *principalID, *limit)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "retire":
		flags := flag.NewFlagSet("model identity key retire", flag.ContinueOnError)
		flags.SetOutput(stderr)
		actorRole := flags.String("actor-role", "", "administrative actor role")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 || strings.TrimSpace(*actorRole) == "" {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "model execution identity key")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Keys.Retire(ctx, *actorRole, id)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	case "revoke":
		flags := flag.NewFlagSet("model identity key revoke", flag.ContinueOnError)
		flags.SetOutput(stderr)
		actorRole := flags.String("actor-role", "", "administrative actor role")
		reason := flags.String("reason", "", "bounded revocation reason code")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 || strings.TrimSpace(*actorRole) == "" || strings.TrimSpace(*reason) == "" {
			return exitUsage
		}
		id, err := positiveID(flags.Arg(0), "model execution identity key")
		if err != nil {
			return exitUsage
		}
		value, err := runtime.Keys.Revoke(ctx, *actorRole, id, *reason)
		if err != nil {
			return modelError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, value)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown model identity key command %q\n", args[0])
		return exitUsage
	}
}

func printModelIdentityUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl model identity <policy|key> ...")
	fmt.Fprintln(out, "  orgctl model identity policy <validate|diff|sync|status> [--json] [--apply]")
	fmt.Fprintln(out, "  orgctl model identity key <register|rotate|get|list|retire|revoke> ...")
}
