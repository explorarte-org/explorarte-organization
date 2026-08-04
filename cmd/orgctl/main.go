package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, buildinfo.Info{Version: version, Commit: commit, BuildTime: buildTime}.String())
		return 0
	case "health":
		return runHealth(args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "up" && args[0] != "status") {
		fmt.Fprintln(stderr, "usage: orgctl migrate <up|status>")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.MigrationTimeout)
	defer cancel()
	store, err := postgres.Open(ctx, cfg.Database, cfg.App.Name+"-migration")
	if err != nil {
		fmt.Fprintf(stderr, "open PostgreSQL: %v\n", err)
		return 1
	}
	defer store.Close()
	if err := postgres.PingWithTimeout(ctx, store, cfg.Database.ConnectTimeout); err != nil {
		fmt.Fprintf(stderr, "PostgreSQL unavailable: %v\n", err)
		return 1
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		fmt.Fprintf(stderr, "create migration runner: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if args[0] == "up" {
		result, err := runner.Up(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "apply migrations: %v\n", err)
			return 1
		}
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(stderr, "encode migration result: %v\n", err)
			return 1
		}
		return 0
	}
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return 1
	}
	if err := encoder.Encode(status); err != nil {
		fmt.Fprintf(stderr, "encode migration status: %v\n", err)
		return 1
	}
	if !status.Ready {
		return 1
	}
	return 0
}

func runHealth(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baseURL := flags.String("url", "http://127.0.0.1:8080", "base URL for orgd")
	ready := flags.Bool("ready", false, "check readiness instead of liveness")
	timeout := flags.Duration("timeout", 3*time.Second, "request timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "timeout must be greater than zero")
		return 2
	}
	endpoint := "/healthz"
	if *ready {
		endpoint = "/readyz"
	}
	requestURL := strings.TrimRight(*baseURL, "/") + endpoint
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		fmt.Fprintf(stderr, "build health request: %v\n", err)
		return 2
	}
	client := &http.Client{Timeout: *timeout}
	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintf(stderr, "health request failed: %v\n", err)
		return 1
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		fmt.Fprintf(stderr, "read health response: %v\n", err)
		return 1
	}
	if len(body) > 0 {
		fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fmt.Fprintf(stderr, "health endpoint returned %s\n", response.Status)
		return 1
	}
	return 0
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl <command> [options]")
	fmt.Fprintln(out, "commands:")
	fmt.Fprintln(out, "  version                print build information")
	fmt.Fprintln(out, "  health [--ready]       check orgd liveness or readiness")
	fmt.Fprintln(out, "  migrate up             apply pending PostgreSQL migrations")
	fmt.Fprintln(out, "  migrate status         inspect PostgreSQL migration status")
}
