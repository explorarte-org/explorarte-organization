package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
)

func runBudget(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printBudgetUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "status":
		return runBudgetStatus(args[1:], stdout, stderr)
	case "set-price":
		return runBudgetSetPrice(args[1:], stdout, stderr)
	case "set-balance":
		return runBudgetSetBalance(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown budget subcommand %q\n", args[0])
		printBudgetUsage(stderr)
		return exitUsage
	}
}

func runBudgetStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budget status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	taskID := flags.Int64("task", 0, "task id to resolve the budget for")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *taskID <= 0 || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl budget status --task <id> [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "budget")
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

	ledger, err := agentbudgetpostgres.New(store)
	if err != nil {
		fmt.Fprintf(stderr, "create agent budget ledger: %v\n", err)
		return exitInternal
	}
	budget, err := ledger.ResolveBudgetForTask(ctx, *taskID)
	if err != nil {
		if errors.Is(err, agentbudget.ErrBudgetNotFound) {
			fmt.Fprintf(stderr, "task %d has no budget attached\n", *taskID)
			return exitInternal
		}
		fmt.Fprintf(stderr, "resolve budget: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, budget)
	return exitOK
}

func runBudgetSetPrice(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budget set-price", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	providerID := flags.String("provider", "", "provider id")
	providerModelID := flags.String("model", "", "provider model id")
	contextTier := flags.String("context-tier", "default", "context tier name")
	minInputTokens := flags.Int64("min-input-tokens", 0, "minimum input tokens for this tier to apply")
	inputUSD := flags.Float64("input", -1, "USD per 1,000,000 input tokens")
	cachedInputUSD := flags.Float64("cached-input", -1, "USD per 1,000,000 cached-input tokens (omit if unpriced)")
	cacheWriteUSD := flags.Float64("cache-write", -1, "USD per 1,000,000 cache-write tokens (omit if unpriced)")
	outputUSD := flags.Float64("output", -1, "USD per 1,000,000 output tokens")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *providerID == "" || *providerModelID == "" || *inputUSD < 0 || *outputUSD < 0 || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl budget set-price --provider <id> --model <id> --input <usd_per_million> --output <usd_per_million> [--context-tier default] [--min-input-tokens 0] [--cached-input <usd_per_million>] [--cache-write <usd_per_million>] [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "budget")
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

	pricingStore, err := modelpricingpostgres.New(store)
	if err != nil {
		fmt.Fprintf(stderr, "create model pricing store: %v\n", err)
		return exitInternal
	}
	service, err := modelpricing.NewService(pricingStore)
	if err != nil {
		fmt.Fprintf(stderr, "create model pricing service: %v\n", err)
		return exitInternal
	}
	tier := modelpricing.PriceTier{
		ProviderID: *providerID, ProviderModelID: *providerModelID, ContextTierName: *contextTier,
		MinInputTokens: *minInputTokens, InputPriceNanosPerMillion: modelpricing.USDFromDollars(*inputUSD),
		OutputPriceNanosPerMillion: modelpricing.USDFromDollars(*outputUSD), EffectiveAt: time.Now().UTC(),
	}
	if *cachedInputUSD >= 0 {
		value := modelpricing.USDFromDollars(*cachedInputUSD)
		tier.CachedInputPriceNanosPerMillion = &value
	}
	if *cacheWriteUSD >= 0 {
		value := modelpricing.USDFromDollars(*cacheWriteUSD)
		tier.CacheWritePriceNanosPerMillion = &value
	}
	saved, err := service.Upsert(ctx, tier)
	if err != nil {
		fmt.Fprintf(stderr, "set price: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, saved)
	return exitOK
}

func runBudgetSetBalance(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budget set-balance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	providerID := flags.String("provider", "", "provider id")
	usd := flags.Float64("usd", -1, "new absolute wallet balance in USD")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *providerID == "" || *usd < 0 || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl budget set-balance --provider <id> --usd <amount> [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "budget")
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
	wallet, err := ledger.SetBalance(ctx, *providerID, modelpricing.USDFromDollars(*usd), time.Now().UTC())
	if err != nil {
		fmt.Fprintf(stderr, "set balance: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, wallet)
	return exitOK
}

func printBudgetUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl budget <status|set-price|set-balance> [flags]

  status --task <id> [--json]
      Show the multidimensional budget (USD, tokens, model calls, wall
      time, depth, retries, subagents — used vs max) a given task resolves
      to, whether it owns that budget row or shares an ancestor's.

  set-price --provider <id> --model <id> --input <usd_per_million>
             --output <usd_per_million> [--context-tier default]
             [--min-input-tokens 0] [--cached-input <usd_per_million>]
             [--cache-write <usd_per_million>] [--json]
      Add a new versioned price tier. Existing tiers are never mutated —
      this always creates a new row effective now.

  set-balance --provider <id> --usd <amount> [--json]
      Set a provider wallet's absolute balance (not a top-up delta).
      Rejected if the new balance is below what is already reserved.`)
}
