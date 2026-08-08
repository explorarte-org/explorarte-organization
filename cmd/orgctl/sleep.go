package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive/sleep"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
)

const defaultSleepWindow = 720 * time.Hour

func runSleep(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		printSleepUsage(stderr)
		return exitUsage
	}
	flags := flag.NewFlagSet("sleep run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	window := flags.Duration("window", defaultSleepWindow, "lookback window for durable experiences")
	if err := flags.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		printSleepUsage(stderr)
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "sleep")
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

	ragRuntime, err := ragbootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "create RAG runtime: %v\n", err)
		return exitInternal
	}
	reader, err := sleep.NewPostgresReader(store, ragRuntime.Manager)
	if err != nil {
		fmt.Fprintf(stderr, "create sleep experience reader: %v\n", err)
		return exitInternal
	}
	service, err := sleep.NewService(reader, ragRuntime.Manager, sleep.ClockFunc(time.Now), sleep.DefaultConfig())
	if err != nil {
		fmt.Fprintf(stderr, "create sleep service: %v\n", err)
		return exitInternal
	}
	result, err := service.RunCycle(ctx, cfg.Tasks.OrganizationID, *window)
	if err != nil {
		fmt.Fprintf(stderr, "run sleep consolidation: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, result)
	return exitOK
}

func printSleepUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl sleep run [--json] [--window=720h]

Runs one bounded offline organizational consolidation cycle over durable
completion-verification evidence. Recurrent evidence-backed patterns may be
proposed as RAG candidates for later review; this command never approves,
reindexes, or publishes knowledge and invokes no model.`)
}
