package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive/smoke"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// runExecutiveSmoke drives internal/executive/smoke — a non-destructive
// proof that the CEO -> leader -> worker -> leader -> CEO executive
// messaging/principal chain works against the live database, safe to run
// against production. It creates exactly 3 support tasks (born in a
// terminal, non-executable status) and 4 agent_messages rows, all tagged
// with one smoke/ correlation id; it never TRUNCATEs, resets sequences,
// resyncs the registry, or modifies any pre-existing row. See
// internal/executive/smoke's package doc for the full design rationale.
//
// This calls smoke.Execute, which runs Preflight (registry-synchronized +
// capability checks, read-only) before creating anything, and guarantees
// Cleanup runs on any failure after that point -- so a failed smoke run
// never leaves messages sitting in 'pending'/'claimed' for a real consumer
// to pick up later.
func runExecutiveSmoke(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("executive smoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	ceoRole := flags.String("ceo-role", "empresa/ceo", "real, already-registered CEO role id")
	leaderRole := flags.String("leader-role", "ingenieria_ia/orquestador", "real, already-registered department leader role id")
	workerRole := flags.String("worker-role", "ingenieria_ia/qa", "real, already-registered worker role id")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl executive smoke [--ceo-role ID] [--leader-role ID] [--worker-role ID] [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	store, err := platformpostgres.Open(ctx, cfg.Database, cfg.App.Name+"-executive-smoke")
	if err != nil {
		fmt.Fprintf(stderr, "open PostgreSQL: %v\n", err)
		return exitInternal
	}
	defer store.Close()

	toolkit, err := smoke.WireToolkit(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "wire executive messaging toolkit: %v\n", err)
		return exitInternal
	}

	roles := smoke.Roles{CEO: *ceoRole, Leader: *leaderRole, Worker: *workerRole}
	report, execErr := smoke.Execute(ctx, store.Pool(), toolkit, roles, time.Now())

	writeExecutiveValue(stdout, *jsonOutput, map[string]any{
		"report": report,
		"error":  errString(execErr),
		"passed": report.Passed,
	})
	if !report.Passed {
		return exitInternal
	}
	return exitOK
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
