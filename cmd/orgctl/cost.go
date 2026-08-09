package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
)

func runCost(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCostUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "calls":
		return runCostCalls(args[1:], stdout, stderr)
	case "summary":
		return runCostSummary(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown cost subcommand %q\n", args[0])
		printCostUsage(stderr)
		return exitUsage
	}
}

func runCostCalls(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cost calls", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	providerID := flags.String("provider", "", "provider id")
	limit := flags.Int("limit", 50, "maximum events to show, newest first")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *providerID == "" || *limit <= 0 || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl cost calls --provider <id> [--limit 50] [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "cost")
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

	ledger, err := costledgerpostgres.New(store)
	if err != nil {
		fmt.Fprintf(stderr, "create provider wallet ledger: %v\n", err)
		return exitInternal
	}
	events, err := ledger.ListEvents(ctx, *providerID, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "list events: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, events)
	return exitOK
}

func runCostSummary(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cost summary", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	providerID := flags.String("provider", "", "provider id")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *providerID == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl cost summary --provider <id> [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "cost")
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

	ledger, err := costledgerpostgres.New(store)
	if err != nil {
		fmt.Fprintf(stderr, "create provider wallet ledger: %v\n", err)
		return exitInternal
	}
	wallet, err := ledger.GetWallet(ctx, *providerID)
	if err != nil {
		fmt.Fprintf(stderr, "get wallet: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, wallet)
	return exitOK
}

func printCostUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl cost <calls|summary> [flags]

  calls --provider <id> [--limit 50] [--json]
      List a provider's most recent wallet events (reserved, committed,
      released), newest first.

  summary --provider <id> [--json]
      Show a provider wallet's current balance and reserved amount.`)
}
