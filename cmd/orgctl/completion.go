package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
)

func printCompletionUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: orgctl completion verify -task <id> -attempt <id> [-json]")
}

// runCompletion is R16's composition root: it never imports internal/tasks,
// internal/staging, internal/authorization or internal/decisiongraph's Go
// packages — completionpostgres.Store reads their tables directly, the same
// pattern internal/decisiongraphtrace already established for reading
// internal/decisiongraph alone. Nothing here mutates any of those packages'
// state; "verify" only reports a verdict for the caller to act on.
func runCompletion(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCompletionUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "completion")
	if code != exitOK {
		return code
	}
	defer store.Close()
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return exitInternal
	}
	if !status.Ready {
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return exitDrift
	}
	reader, err := completionpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create completion reader: %v\n", err)
		return exitInternal
	}
	service, err := completion.NewService(reader, reader, reader, reader, reader, nil)
	if err != nil {
		fmt.Fprintf(stderr, "create completion service: %v\n", err)
		return exitInternal
	}

	switch args[0] {
	case "verify":
		return completionVerify(ctx, service, args[1:], stdout, stderr)
	default:
		printCompletionUsage(stderr)
		return exitUsage
	}
}

func completionVerify(ctx context.Context, service *completion.Service, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("completion verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	taskID := flags.Int64("task", 0, "task ID")
	attemptID := flags.Int64("attempt", 0, "attempt ID")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil {
		return exitUsage
	}
	if *taskID <= 0 || *attemptID <= 0 {
		fmt.Fprintln(stderr, "completion verify: -task and -attempt are required and must be positive")
		return exitUsage
	}
	result, err := service.Verify(ctx, completion.VerificationRequest{TaskID: *taskID, AttemptID: *attemptID})
	if err != nil {
		fmt.Fprintf(stderr, "completion verify failed: %v\n", err)
		return exitInvalid
	}
	writeValue(stdout, *jsonOutput, result)
	switch result.Verdict {
	case completion.VerdictPass:
		return exitOK
	case completion.VerdictInconclusive:
		return exitCompletionInconclusive
	default:
		return exitCompletionFailed
	}
}
