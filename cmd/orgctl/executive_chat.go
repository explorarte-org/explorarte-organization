package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	ceochatbootstrap "github.com/Mireuz13/explorarte-organization/internal/ceochat/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	executivebootstrap "github.com/Mireuz13/explorarte-organization/internal/executive/bootstrap"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// chatModelCallDeadline mirrors executiveModelCallDeadline: a chat turn may
// call a real provider and, within the same run, a bounded number of tool
// calls, so it needs the same generous headroom a typed-task run does.
const chatModelCallDeadline = executiveModelCallDeadline

const maxChatMessageBytes = 32 << 10

func runExecutiveChat(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printExecutiveChatUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "create":
		return runExecutiveChatCreate(args[1:], stdout, stderr)
	case "send":
		return runExecutiveChatSend(args[1:], stdout, stderr)
	case "history":
		return runExecutiveChatHistory(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printExecutiveChatUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown executive chat command %q\n", args[0])
		printExecutiveChatUsage(stderr)
		return exitUsage
	}
}

func printExecutiveChatUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl executive chat <command> [options]
commands:
  create --actor-role empresa/human --owner-role empresa/human [--json]
  send CONVERSATION_ID --actor-role empresa/human --idempotency-key KEY [--file message.txt] [--json]
  history CONVERSATION_ID --actor-role empresa/human [--limit 32] [--json]`)
}

func runExecutiveChatCreate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive chat create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actorRole := flags.String("actor-role", "", "requesting owner role")
	ownerRole := flags.String("owner-role", "", "conversation owner role (defaults to --actor-role)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 0 || *actorRole == "" {
		fmt.Fprintln(stderr, "usage: orgctl executive chat create --actor-role empresa/human [--owner-role empresa/human] [--json]")
		return exitUsage
	}
	owner := *ownerRole
	if owner == "" {
		owner = *actorRole
	}
	cfg, runtime, store, ctx, cancel, code := openCeoChatRuntime(stderr, "chat-create", chatModelCallDeadline)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	_ = cfg
	conversation, err := runtime.Service.CreateConversation(ctx, ceochat.CreateConversationRequest{
		ActorRoleID: *actorRole, OwnerRoleID: owner,
	})
	if err != nil {
		fmt.Fprintf(stderr, "create chat conversation: %v\n", err)
		return chatExitCode(err)
	}
	writeChatValue(stdout, *jsonOutput, conversation)
	return exitOK
}

func runExecutiveChatSend(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive chat send", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actorRole := flags.String("actor-role", "", "sending owner role")
	idempotencyKey := flags.String("idempotency-key", "", "stable per-message key")
	file := flags.String("file", "", "message file (default: stdin)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 1 || *actorRole == "" || *idempotencyKey == "" {
		fmt.Fprintln(stderr, "usage: orgctl executive chat send CONVERSATION_ID --actor-role empresa/human --idempotency-key KEY [--file message.txt] [--json]")
		return exitUsage
	}
	conversationID, err := positiveID(flags.Arg(0), "CONVERSATION_ID")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	content, err := readBoundedMessage(*file)
	if err != nil {
		fmt.Fprintf(stderr, "read message: %v\n", err)
		return exitInvalid
	}
	_, runtime, store, ctx, cancel, code := openCeoChatRuntime(stderr, "chat-send", chatModelCallDeadline)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	result, err := runtime.Service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversationID, ActorRoleID: *actorRole, IdempotencyKey: *idempotencyKey, Content: content,
	})
	if err != nil {
		fmt.Fprintf(stderr, "send chat message: %v\n", err)
		return chatExitCode(err)
	}
	writeChatValue(stdout, *jsonOutput, result)
	return exitOK
}

func runExecutiveChatHistory(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive chat history", flag.ContinueOnError)
	flags.SetOutput(stderr)
	actorRole := flags.String("actor-role", "", "requesting owner role (must be the conversation's owner)")
	limit := flags.Int("limit", ceochat.DefaultHistoryMessageLimit, "maximum messages to return")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 1 || *actorRole == "" {
		fmt.Fprintln(stderr, "usage: orgctl executive chat history CONVERSATION_ID --actor-role empresa/human [--limit 32] [--json]")
		return exitUsage
	}
	conversationID, err := positiveID(flags.Arg(0), "CONVERSATION_ID")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	_, runtime, store, ctx, cancel, code := openCeoChatRuntime(stderr, "chat-history", 30*time.Second)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	messages, err := runtime.Service.History(ctx, ceochat.HistoryRequest{ConversationID: conversationID, ActorRoleID: *actorRole, Limit: *limit})
	if err != nil {
		fmt.Fprintf(stderr, "chat history: %v\n", err)
		return chatExitCode(err)
	}
	writeChatValue(stdout, *jsonOutput, messages)
	return exitOK
}

func readBoundedMessage(path string) (string, error) {
	reader, closeFn, err := inputReader(path)
	if err != nil {
		return "", err
	}
	defer closeFn()
	body, err := io.ReadAll(io.LimitReader(reader, maxChatMessageBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxChatMessageBytes {
		return "", fmt.Errorf("message exceeds %d bytes", maxChatMessageBytes)
	}
	if len(body) == 0 {
		return "", errors.New("message is empty")
	}
	return string(body), nil
}

func openCeoChatRuntime(stderr io.Writer, suffix string, timeout time.Duration) (config.Config, *ceochatbootstrap.Runtime, *platformpostgres.Store, context.Context, context.CancelFunc, int) {
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
	// campaign.promote_to_executive needs a real campaign.ExecutiveSubmitter
	// to do anything but fail closed with "promotion service is not
	// configured" -- without this, every owner "lánzala" ever reaches a
	// dead end no matter how correct the rest of the campaign chain is.
	// This process therefore opens two composition roots (Executive's and
	// ceochat's own) -- but WithModelRuntime makes that ONE Model Runtime,
	// not two: executiveRuntime.Models is threaded straight into ceochat's
	// own Open instead of letting it construct an independent one (two
	// provider adapter sets, two routers, two circuit breakers, two egress
	// clients, even though both would otherwise talk to the same
	// database -- durable storage equality is not runtime equality).
	// TestCEOChatSharesExecutiveModelRuntime proves this by pointer
	// identity against real PostgreSQL.
	executiveRuntime, err := executivebootstrap.Open(cfg, store)
	if err != nil {
		store.Close()
		cancel()
		fmt.Fprintf(stderr, "open executive runtime for campaign promotion: %v\n", err)
		return cfg, nil, nil, nil, func() {}, exitInternal
	}
	runtime, err := ceochatbootstrap.Open(cfg, store,
		ceochatbootstrap.WithModelRuntime(executiveRuntime.Models),
		ceochatbootstrap.WithExecutiveSubmitter(executiveRuntime.Orchestrator),
	)
	if err != nil {
		store.Close()
		cancel()
		fmt.Fprintf(stderr, "open ceo chat runtime: %v\n", err)
		return cfg, nil, nil, nil, func() {}, exitInternal
	}
	return cfg, runtime, store, ctx, cancel, exitOK
}

func chatExitCode(err error) int {
	switch {
	case errors.Is(err, ceochat.ErrInvalidInput):
		return exitInvalid
	case errors.Is(err, ceochat.ErrUnauthorizedActor):
		return exitDenied
	case errors.Is(err, ceochat.ErrConversationNotFound):
		return exitInvalid
	case errors.Is(err, ceochat.ErrIdempotencyConflict):
		return exitInvalid
	case errors.Is(err, ceochat.ErrRunNotReady):
		return exitApprovalRequired
	default:
		return exitInternal
	}
}

func writeChatValue(out io.Writer, jsonOutput bool, value any) {
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(value)
		return
	}
	switch v := value.(type) {
	case ceochat.Conversation:
		fmt.Fprintf(out, "conversation_id=%d owner_role_id=%s status=%s\n", v.ID, v.OwnerRoleID, v.Status)
	case ceochat.SendResult:
		fmt.Fprintf(out, "reused=%v outcome=%s owner_message_id=%d", v.Reused, v.Outcome, v.OwnerMessage.ID)
		if v.AssistantMessage != nil {
			fmt.Fprintf(out, " assistant_message_id=%d\n%s\n", v.AssistantMessage.ID, v.AssistantMessage.Content)
			return
		}
		fmt.Fprintln(out)
	default:
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(value)
	}
}
