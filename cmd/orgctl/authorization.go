package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
)

func runAuthorization(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAuthorizationUsage(stderr)
		return exitUsage
	}
	cfg, runtime, cleanup, code := openAuthorizationRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Authorization.CommandTimeout)
	defer cancel()
	switch args[0] {
	case "evaluate":
		return authorizationEvaluate(ctx, runtime, args[1:], stdout, stderr)
	case "request":
		return authorizationRequest(ctx, runtime, args[1:], stdout, stderr)
	case "get":
		return authorizationGet(ctx, runtime, args[1:], stdout, stderr)
	case "list":
		return authorizationList(ctx, runtime, args[1:], stdout, stderr)
	case "decide":
		return authorizationDecide(ctx, runtime, args[1:], stdout, stderr)
	case "consume":
		return authorizationConsume(ctx, runtime, args[1:], stdout, stderr)
	case "cancel":
		return authorizationCancel(ctx, runtime, args[1:], stdout, stderr)
	case "expire":
		return authorizationExpire(ctx, runtime, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown authorization command %q\n", args[0])
		printAuthorizationUsage(stderr)
		return exitUsage
	}
}

func openAuthorizationRuntime(stderr io.Writer) (config.Config, *authorizationbootstrap.Runtime, func(), int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return config.Config{}, nil, func() {}, exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Authorization.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "authorization")
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
	runtime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		cleanup()
		fmt.Fprintf(stderr, "open authorization runtime: %v\n", err)
		return config.Config{}, nil, func() {}, exitInternal
	}
	return cfg, runtime, cleanup, exitOK
}

func authorizationEvaluate(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization evaluate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "actor role")
	capability := flags.String("capability", "", "capability ID")
	resourceType := flags.String("resource-type", "", "resource type")
	resourceID := flags.String("resource-id", "", "resource ID")
	actionDigest := flags.String("action-digest", "", "canonical action SHA-256")
	approvalID := flags.Int64("approval-request", 0, "approval request ID")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	revision, err := runtime.Service.CurrentRevision(ctx)
	if err != nil {
		return authorizationError(stderr, err)
	}
	request := authorization.EvaluationRequest{OrganizationID: runtime.Service.OrganizationID(), OrganizationRevisionID: revision, ActorRoleID: *actor, CapabilityID: *capability, ResourceType: *resourceType, ResourceID: *resourceID, ActionDigest: *actionDigest}
	if *approvalID != 0 {
		request.ApprovalRequestID = approvalID
	}
	value, err := runtime.Service.Evaluate(ctx, request)
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	switch value.Effect {
	case authorization.EffectAllow:
		return exitOK
	case authorization.EffectDeny:
		return exitDenied
	case authorization.EffectApprovalRequired:
		return exitApprovalRequired
	default:
		return exitInternal
	}
}

func authorizationRequest(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization request", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "requester role")
	capability := flags.String("capability", "", "capability ID")
	resourceType := flags.String("resource-type", "", "resource type")
	resourceID := flags.String("resource-id", "", "resource ID")
	actionDigest := flags.String("action-digest", "", "canonical action SHA-256")
	idempotency := flags.String("idempotency-key", "", "idempotency key")
	reason := flags.String("reason", "", "request reason")
	ttl := flags.Duration("ttl", 0, "approval TTL")
	correlation := flags.String("correlation-id", "", "correlation ID")
	causation := flags.String("causation-id", "", "causation ID")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	value, err := runtime.Service.RequestApproval(ctx, authorization.RequestApprovalCommand{ActorRoleID: *actor, CapabilityID: *capability, ResourceType: *resourceType, ResourceID: *resourceID, ActionDigest: *actionDigest, IdempotencyKey: *idempotency, Reason: *reason, TTL: *ttl, CorrelationID: *correlation, CausationID: *causation})
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func authorizationGet(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	jsonOutput, rest, code := parseJSONOnly("authorization get", args, stderr)
	if code != exitOK || len(rest) != 1 {
		return exitUsage
	}
	id, err := positiveID(rest[0], "authorization request")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := runtime.Service.GetRequest(ctx, id)
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, jsonOutput, value)
	return exitOK
}

func authorizationList(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	status := flags.String("status", "", "request status")
	actor := flags.String("actor-role", "", "requester role")
	capability := flags.String("capability", "", "capability ID")
	limit := flags.Int("limit", 100, "result limit")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	values, err := runtime.Service.ListRequests(ctx, authorization.ListRequestsFilter{Status: authorization.RequestStatus(*status), RequesterRoleID: *actor, CapabilityID: *capability, Limit: *limit})
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, values)
	return exitOK
}

func authorizationDecide(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization decide", flag.ContinueOnError)
	flags.SetOutput(stderr)
	decision := flags.String("decision", "", "approve or reject")
	actor := flags.String("actor-role", "", "owner role")
	reason := flags.String("reason", "", "decision reason")
	reference := flags.String("reference", "", "opaque reference")
	digest := flags.String("digest", "", "optional SHA-256")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "authorization request")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := runtime.Service.DecideRequest(ctx, authorization.DecideRequestCommand{RequestID: id, Decision: authorization.Decision(*decision), ActorRoleID: *actor, Reason: *reason, Reference: *reference, Digest: *digest})
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func authorizationConsume(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization consume", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "requester role")
	digest := flags.String("action-digest", "", "canonical action SHA-256")
	correlation := flags.String("correlation-id", "", "correlation ID")
	causation := flags.String("causation-id", "", "causation ID")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "authorization request")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := runtime.Service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: id, ActorRoleID: *actor, ActionDigest: *digest, CorrelationID: *correlation, CausationID: *causation})
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func authorizationCancel(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization cancel", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actor := flags.String("actor-role", "", "requester or owner role")
	reason := flags.String("reason", "", "cancellation reason")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "authorization request")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	value, err := runtime.Service.CancelRequest(ctx, authorization.CancelRequestCommand{RequestID: id, ActorRoleID: *actor, Reason: *reason})
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func authorizationExpire(ctx context.Context, runtime *authorizationbootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("authorization expire", flag.ContinueOnError)
	flags.SetOutput(stderr)
	batch := flags.Int("batch", 0, "expiration batch")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	value, err := runtime.Service.ExpireRequests(ctx, *batch)
	if err != nil {
		return authorizationError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, value)
	return exitOK
}

func authorizationError(stderr io.Writer, err error) int {
	message := err.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	fmt.Fprintln(stderr, message)
	switch {
	case errors.Is(err, authorization.ErrDatabaseUnavailable):
		return exitDatabase
	case errors.Is(err, authorization.ErrInvalidInput):
		return exitUsage
	case errors.Is(err, authorization.ErrApprovalRequired), errors.Is(err, authorization.ErrApprovalPending):
		return exitApprovalRequired
	case errors.Is(err, authorization.ErrCapabilityDenied), errors.Is(err, authorization.ErrUnknownCapability), errors.Is(err, authorization.ErrUnknownAuthorityClass), errors.Is(err, authorization.ErrPolicyRevisionMismatch), errors.Is(err, authorization.ErrOrganizationMismatch), errors.Is(err, authorization.ErrRoleNotFound), errors.Is(err, authorization.ErrRoleDisabled), errors.Is(err, authorization.ErrRoleRetired), errors.Is(err, authorization.ErrRoleNotExecutable), errors.Is(err, authorization.ErrApproverNotOwner), errors.Is(err, authorization.ErrApprovalRejected), errors.Is(err, authorization.ErrApprovalExpired), errors.Is(err, authorization.ErrApprovalConsumed), errors.Is(err, authorization.ErrApprovalCancelled), errors.Is(err, authorization.ErrApprovalScopeMismatch), errors.Is(err, authorization.ErrApprovalPolicyDrift):
		return exitDenied
	case errors.Is(err, authorization.ErrRequestNotFound):
		return exitInvalid
	case errors.Is(err, authorization.ErrIdempotencyConflict), errors.Is(err, authorization.ErrInvalidTransition):
		return exitDrift
	default:
		return exitInternal
	}
}

func printAuthorizationUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl authorization <evaluate|request|get|list|decide|consume|cancel|expire> ...")
}
