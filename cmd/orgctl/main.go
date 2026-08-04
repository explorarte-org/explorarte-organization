package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "version":
		info := buildinfo.Info{Version: version, Commit: commit, BuildTime: buildTime}
		fmt.Fprintln(stdout, info.String())
		return 0
	case "health":
		return runHealth(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
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

	response, err := http.DefaultClient.Do(request)
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
}
