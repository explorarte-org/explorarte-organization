package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
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

const (
	exitOK               = 0
	exitInternal         = 1
	exitUsage            = 2
	exitDrift            = 3
	exitInvalid          = 4
	exitDatabase         = 5
	exitDenied           = 6
	exitApprovalRequired = 7
	exitContextRejected  = 8
	exitContextStale     = 9
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, buildinfo.Info{Version: version, Commit: commit, BuildTime: buildTime}.String())
		return exitOK
	case "health":
		return runHealth(args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(args[1:], stdout, stderr)
	case "registry":
		return runRegistry(args[1:], stdout, stderr)
	case "task":
		return runTask(args[1:], stdout, stderr)
	case "outbox":
		return runOutbox(args[1:], stdout, stderr)
	case "staging":
		return runStaging(args[1:], stdout, stderr)
	case "authorization":
		return runAuthorization(args[1:], stdout, stderr)
	case "context":
		return runContext(args[1:], stdout, stderr)
	case "model":
		return runModel(args[1:], stdout, stderr)
	case "decision":
		return runDecision(args[1:], stdout, stderr)
	case "improvement":
		return runImprovement(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

func runMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "up" && args[0] != "status") {
		fmt.Fprintln(stderr, "usage: orgctl migrate <up|status>")
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.MigrationTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "migration")
	if code != exitOK {
		return code
	}
	defer store.Close()
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if args[0] == "up" {
		result, err := runner.Up(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "apply migrations: %v\n", err)
			return exitInternal
		}
		if err = encoder.Encode(result); err != nil {
			return encodeError(stderr, err)
		}
		return exitOK
	}
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return exitInternal
	}
	if err = encoder.Encode(status); err != nil {
		return encodeError(stderr, err)
	}
	if !status.Ready {
		return exitDrift
	}
	return exitOK
}

func runRegistry(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRegistryUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		fmt.Fprintf(stderr, "create canonical loader: %v\n", err)
		return exitUsage
	}
	switch args[0] {
	case "validate":
		return registryValidate(loader, args[1:], stdout, stderr)
	case "diff", "sync", "status", "list-units", "list-roles", "get-role", "get-leader":
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Registry.SyncTimeout)
		defer cancel()
		store, runner, code := openDatabase(ctx, cfg, stderr, "registry")
		if code != exitOK {
			return code
		}
		defer store.Close()
		status, e := runner.Status(ctx)
		if e != nil {
			fmt.Fprintf(stderr, "migration status: %v\n", e)
			return exitInternal
		}
		if !status.Ready {
			fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
			return exitDrift
		}
		canonical, report, e := loader.Load()
		if e != nil {
			var validation *registry.ValidationError
			if errors.As(e, &validation) {
				for _, issue := range validation.Report.Errors {
					fmt.Fprintf(stderr, "invalid %s %s: %s\n", issue.Code, issue.Path, issue.Message)
				}
				return exitInvalid
			}
			fmt.Fprintf(stderr, "load canonical registry: %v\n", e)
			return exitInternal
		}
		_ = report
		repository, e := registry.NewPostgresRepository(store)
		if e != nil {
			fmt.Fprintf(stderr, "create registry repository: %v\n", e)
			return exitInternal
		}
		service, e := registry.NewService(loader, repository, canonical.Organization.ID, cfg.Registry.SyncTimeout)
		if e != nil {
			fmt.Fprintf(stderr, "create registry service: %v\n", e)
			return exitInternal
		}
		return executeRegistryCommand(ctx, service, args, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown registry command %q\n", args[0])
		printRegistryUsage(stderr)
		return exitUsage
	}
}

func registryValidate(loader *registry.Loader, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("registry validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		return exitUsage
	}
	snapshot, report, err := loader.Load()
	if err != nil {
		var validation *registry.ValidationError
		if errors.As(err, &validation) {
			writeValue(stdout, *jsonOutput, validation.Report)
			return exitInvalid
		}
		fmt.Fprintf(stderr, "validate canonical documents: %v\n", err)
		return exitInternal
	}
	if *jsonOutput {
		writeValue(stdout, true, map[string]any{"valid": true, "canonical_hash": snapshot.CanonicalHash, "counts": snapshot.Counts, "warnings": report.Warnings})
	} else {
		fmt.Fprintf(stdout, "valid canonical registry %s: %d units, %d roles, %d reporting lines\n", snapshot.CanonicalHash, snapshot.Counts.Units, snapshot.Counts.Roles, snapshot.Counts.ReportingRelationships)
		printWarnings(stdout, report)
	}
	return exitOK
}

func executeRegistryCommand(ctx context.Context, service *registry.Service, args []string, stdout, stderr io.Writer) int {
	command := args[0]
	switch command {
	case "diff":
		jsonOutput, rest, code := parseJSONOnly(command, args[1:], stderr)
		if code != 0 {
			return code
		}
		if len(rest) != 0 {
			return exitUsage
		}
		comparison, err := service.CompareCanonical(ctx)
		if err != nil {
			return registryError(stderr, err)
		}
		writeValue(stdout, jsonOutput, comparison)
		if !comparison.Synchronized {
			return exitDrift
		}
		return exitOK
	case "sync":
		flags := flag.NewFlagSet("registry sync", flag.ContinueOnError)
		flags.SetOutput(stderr)
		apply := flags.Bool("apply", false, "apply canonical revision")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if flags.NArg() != 0 {
			return exitUsage
		}
		result, err := service.SynchronizeCanonical(ctx, *apply)
		if err != nil {
			return registryError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, result)
		if result.NoOp || result.Applied {
			return exitOK
		}
		return exitDrift
	case "status":
		jsonOutput, rest, code := parseJSONOnly(command, args[1:], stderr)
		if code != 0 {
			return code
		}
		if len(rest) != 0 {
			return exitUsage
		}
		revision, err := service.GetCurrentRevision(ctx)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				revision = nil
			} else {
				return registryError(stderr, err)
			}
		}
		comparison, err := service.CompareCanonical(ctx)
		if err != nil {
			return registryError(stderr, err)
		}
		value := map[string]any{"revision": revision, "canonical_hash": comparison.Canonical.CanonicalHash, "synchronized": comparison.Synchronized, "diff": comparison.Diff}
		writeValue(stdout, jsonOutput, value)
		if !comparison.Synchronized {
			return exitDrift
		}
		return exitOK
	case "list-units":
		jsonOutput, rest, code := parseJSONOnly(command, args[1:], stderr)
		if code != 0 {
			return code
		}
		if len(rest) != 0 {
			return exitUsage
		}
		values, err := service.ListUnits(ctx)
		if err != nil {
			return registryError(stderr, err)
		}
		writeValue(stdout, jsonOutput, values)
		return exitOK
	case "list-roles":
		flags := flag.NewFlagSet("registry list-roles", flag.ContinueOnError)
		flags.SetOutput(stderr)
		unit := flags.String("unit", "", "filter by unit")
		enabled := flags.Bool("enabled", false, "only enabled roles")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if flags.NArg() != 0 {
			return exitUsage
		}
		if *unit != "" {
			if err := registry.ValidateUnitID(*unit); err != nil {
				fmt.Fprintln(stderr, err)
				return exitUsage
			}
		}
		values, err := service.ListRoles(ctx, registry.RoleFilter{UnitID: *unit, EnabledOnly: *enabled})
		if err != nil {
			return registryError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, values)
		return exitOK
	case "get-role":
		jsonOutput, rest, code := parseJSONOnly(command, args[1:], stderr)
		if code != 0 {
			return code
		}
		if len(rest) != 1 {
			fmt.Fprintln(stderr, "usage: orgctl registry get-role <unit/role> [--json]")
			return exitUsage
		}
		if err := registry.ValidateRoleID(rest[0]); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		value, err := service.GetRole(ctx, rest[0])
		if err != nil {
			return registryError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "get-leader":
		jsonOutput, rest, code := parseJSONOnly(command, args[1:], stderr)
		if code != 0 {
			return code
		}
		if len(rest) != 1 {
			fmt.Fprintln(stderr, "usage: orgctl registry get-leader <unit> [--json]")
			return exitUsage
		}
		if err := registry.ValidateUnitID(rest[0]); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		value, err := service.GetLeader(ctx, rest[0])
		if err != nil {
			return registryError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	default:
		return exitUsage
	}
}

func parseJSONOnly(name string, args []string, stderr io.Writer) (bool, []string, int) {
	jsonOutput := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "-h", "--help":
			fmt.Fprintf(stderr, "usage: orgctl registry %s [--json]\n", name)
			return false, nil, exitUsage
		default:
			rest = append(rest, arg)
		}
	}
	return jsonOutput, rest, exitOK
}

func openDatabase(ctx context.Context, cfg config.Config, stderr io.Writer, suffix string) (*postgres.Store, *platformmigrations.Runner, int) {
	store, err := postgres.Open(ctx, cfg.Database, cfg.App.Name+"-"+suffix)
	if err != nil {
		fmt.Fprintf(stderr, "open PostgreSQL: %v\n", err)
		return nil, nil, exitDatabase
	}
	if err = postgres.PingWithTimeout(ctx, store, cfg.Database.ConnectTimeout); err != nil {
		store.Close()
		fmt.Fprintf(stderr, "PostgreSQL unavailable: %v\n", err)
		return nil, nil, exitDatabase
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "create migration runner: %v\n", err)
		return nil, nil, exitInternal
	}
	return store, runner, exitOK
}

func writeValue(out io.Writer, jsonOutput bool, value any) {
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(value)
		return
	}
	switch values := value.(type) {
	case []registry.Unit:
		for _, v := range values {
			leader := "-"
			if v.LeaderRoleID != nil {
				leader = *v.LeaderRoleID
			}
			fmt.Fprintf(out, "%-20s kind=%-32s operational=%-5t leader=%s\n", v.ID, v.Kind, v.Operational, leader)
		}
	case []registry.Role:
		for _, v := range values {
			fmt.Fprintf(out, "%-48s unit=%-20s enabled=%-5t kind=%s\n", v.ID, v.UnitID, v.Enabled, v.RuntimeKind)
		}
	default:
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(value)
	}
}
func printWarnings(out io.Writer, report registry.ValidationReport) {
	for _, warning := range report.Warnings {
		fmt.Fprintf(out, "warning %s %s: %s\n", warning.Code, warning.Path, warning.Message)
	}
}
func registryError(stderr io.Writer, err error) int {
	var validation *registry.ValidationError
	if errors.As(err, &validation) {
		for _, issue := range validation.Report.Errors {
			fmt.Fprintf(stderr, "invalid %s %s: %s\n", issue.Code, issue.Path, issue.Message)
		}
		return exitInvalid
	}
	if errors.Is(err, registry.ErrNotFound) {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	if registry.IsDatabaseUnavailable(err) {
		fmt.Fprintf(stderr, "registry database unavailable: %v\n", err)
		return exitDatabase
	}
	fmt.Fprintf(stderr, "registry operation failed: %v\n", err)
	return exitInternal
}
func encodeError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "encode result: %v\n", err)
	return exitInternal
}

func runHealth(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baseURL := flags.String("url", "http://127.0.0.1:8080", "base URL for orgd")
	ready := flags.Bool("ready", false, "check readiness")
	timeout := flags.Duration("timeout", 3*time.Second, "request timeout")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *timeout <= 0 {
		return exitUsage
	}
	endpoint := "/healthz"
	if *ready {
		endpoint = "/readyz"
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(*baseURL, "/")+endpoint, nil)
	if err != nil {
		return exitUsage
	}
	response, err := (&http.Client{Timeout: *timeout}).Do(request)
	if err != nil {
		fmt.Fprintf(stderr, "health request failed: %v\n", err)
		return exitInternal
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if len(body) > 0 {
		fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return exitInternal
	}
	return exitOK
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl <command> [options]
commands:
  version
  health [--ready]
  migrate <up|status>
  registry <validate|diff|sync|status|list-units|list-roles|get-role|get-leader>
  task <create|get|list|claim|start|heartbeat|result|finalize|block|unblock|cancel|dependency|requirement|evidence|events|attempts|reconcile|dead-letter>
  outbox <claim|ack|nack|recover|status>
  staging <repo|workspace|check|promotion|reconcile>
  authorization <evaluate|request|get|list|decide|consume|cancel|expire>
  context <validate-source|build|get|list|render|validate|invalidate>
  model <registry|invocation>
  decision <create|append|start|transition|claim|finish|observe|verify|decide|recover|trace>
  improvement <propose|get|validate|begin-evaluation|verdict|promote-canary|promote-active|deprecate|rollback|trace>
exit codes:
  0 success
  1 internal or operational failure
  2 usage error
  3 drift
  4 not found or invalid resource
  5 database unavailable
  6 capability denied
  7 approval required
  8 context rejected by validation or policy
  9 context stale or invalidated`)
}
func printRegistryUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl registry <command> [options]")
}
