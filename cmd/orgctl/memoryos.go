package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/consolidation"
	memoryospostgres "github.com/Mireuz13/explorarte-organization/internal/memoryos/postgres"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
)

const defaultMemoryOSWindow = 720 * time.Hour

func runMemoryOS(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printMemoryOSUsage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "project-run":
		return runMemoryOSProjectRun(args[1:], stdout, stderr)
	case "episode":
		if len(args) < 2 || args[1] != "get" {
			printMemoryOSUsage(stderr)
			return exitUsage
		}
		return runMemoryOSEpisodeGet(args[2:], stdout, stderr)
	case "consolidate":
		return runMemoryOSConsolidate(args[1:], stdout, stderr)
	default:
		printMemoryOSUsage(stderr)
		return exitUsage
	}
}

func printMemoryOSUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: orgctl memoryos <command> [options]")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  project-run <HARNESS_RUN_ID> [--json]")
	fmt.Fprintln(w, "  episode get <EPISODE_ID> [--json]")
	fmt.Fprintln(w, "  consolidate [--window 720h] [--json]")
}

func runMemoryOSProjectRun(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("memoryos project-run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: orgctl memoryos project-run <HARNESS_RUN_ID> [--json]")
		return exitUsage
	}
	runID := strings.TrimSpace(flags.Arg(0))
	if runID == "" {
		fmt.Fprintln(stderr, "memoryos: HARNESS_RUN_ID is required")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "memoryos-project-run")
	if code != exitOK {
		return code
	}
	defer store.Close()
	if status, err := runner.Status(ctx); err != nil || !status.Ready {
		fmt.Fprintf(stderr, "database migrations not ready\n")
		return exitDrift
	}

	memStore, err := memoryospostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "open memoryos store: %v\n", err)
		return exitInternal
	}

	ep, err := memStore.ProjectHarnessRun(ctx, runID)
	if err != nil {
		fmt.Fprintf(stderr, "project harness run: %v\n", err)
		return exitInternal
	}

	if *jsonOutput {
		body, _ := json.MarshalIndent(ep, "", "  ")
		fmt.Fprintln(stdout, string(body))
	} else {
		fmt.Fprintf(stdout, "Projected Episode: %s\n", ep.ID)
		fmt.Fprintf(stdout, "  HarnessRunID: %s\n", ep.HarnessRunID)
		fmt.Fprintf(stdout, "  TaskID: %d AttemptID: %d\n", ep.TaskID, ep.AttemptID)
		fmt.Fprintf(stdout, "  TaskClass: %s Role: %s\n", ep.TaskClass, ep.RoleID)
		fmt.Fprintf(stdout, "  CanonicalDigest: %s\n", ep.CanonicalDigest)
		fmt.Fprintf(stdout, "  Status: %s (Turns: %d, ToolCalls: %d)\n", ep.Status, ep.TurnsUsed, ep.ToolCallsUsed)
	}
	return exitOK
}

func runMemoryOSEpisodeGet(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("memoryos episode get", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: orgctl memoryos episode get <EPISODE_ID> [--json]")
		return exitUsage
	}
	episodeID := strings.TrimSpace(flags.Arg(0))
	if episodeID == "" {
		fmt.Fprintln(stderr, "memoryos: EPISODE_ID is required")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "memoryos-episode-get")
	if code != exitOK {
		return code
	}
	defer store.Close()
	if status, err := runner.Status(ctx); err != nil || !status.Ready {
		fmt.Fprintf(stderr, "database migrations not ready\n")
		return exitDrift
	}

	memStore, err := memoryospostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "open memoryos store: %v\n", err)
		return exitInternal
	}

	ep, ok, err := memStore.GetEpisode(ctx, cfg.Tasks.OrganizationID, episodeID)
	if err != nil {
		fmt.Fprintf(stderr, "get episode: %v\n", err)
		return exitInternal
	}
	if !ok {
		fmt.Fprintf(stderr, "episode %s not found\n", episodeID)
		return exitUsage
	}

	if *jsonOutput {
		body, _ := json.MarshalIndent(ep, "", "  ")
		fmt.Fprintln(stdout, string(body))
	} else {
		fmt.Fprintf(stdout, "Episode: %s\n", ep.ID)
		fmt.Fprintf(stdout, "  HarnessRunID: %s\n", ep.HarnessRunID)
		fmt.Fprintf(stdout, "  TaskID: %d AttemptID: %d\n", ep.TaskID, ep.AttemptID)
		fmt.Fprintf(stdout, "  TaskClass: %s Role: %s\n", ep.TaskClass, ep.RoleID)
		fmt.Fprintf(stdout, "  CanonicalDigest: %s\n", ep.CanonicalDigest)
		fmt.Fprintf(stdout, "  Status: %s (Turns: %d, ToolCalls: %d)\n", ep.Status, ep.TurnsUsed, ep.ToolCallsUsed)
	}
	return exitOK
}

func runMemoryOSConsolidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("memoryos consolidate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	window := flags.Duration("window", defaultMemoryOSWindow, "lookback window for episodes")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl memoryos consolidate [--window 720h] [--json]")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "memoryos-consolidate")
	if code != exitOK {
		return code
	}
	defer store.Close()
	if status, err := runner.Status(ctx); err != nil || !status.Ready {
		fmt.Fprintf(stderr, "database migrations not ready\n")
		return exitDrift
	}

	memStore, err := memoryospostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "open memoryos store: %v\n", err)
		return exitInternal
	}

	ragRuntime, err := ragbootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "create RAG runtime: %v\n", err)
		return exitInternal
	}

	memRuntime, err := memorybootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "create memory runtime: %v\n", err)
		return exitInternal
	}

	consolidationConfig := consolidation.DefaultConfig()
	consolidationConfig.MaxWindow = *window + time.Hour

	service, err := consolidation.NewService(
		memStore, memStore,
		ragRuntime.Manager, memRuntime.Manager,
		consolidationConfig,
	)
	if err != nil {
		fmt.Fprintf(stderr, "create consolidation service: %v\n", err)
		return exitInternal
	}

	now := time.Now().UTC()
	from := now.Add(-*window)
	result, err := service.Consolidate(ctx, cfg.Tasks.OrganizationID, from, now)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "consolidation error: %v\n", err)
		return exitInternal
	}

	if *jsonOutput {
		body, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(body))
	} else {
		fmt.Fprintf(stdout, "MemoryOS Consolidation Results (%s to %s):\n", result.WindowStart.Format(time.RFC3339), result.WindowEnd.Format(time.RFC3339))
		fmt.Fprintf(stdout, "  Episodes Seen: %d (Projected: %d, Reused: %d)\n", result.EpisodesSeen, result.EpisodesProjected, result.EpisodesReused)
		if result.SemanticSkippedNotOwner {
			fmt.Fprintf(stdout, "  Semantic Candidates: 0 (skipped: owner is %s, reason: %s, groups: %d)\n", result.SemanticOwner, result.SemanticSkipReason, result.SemanticGroups)
		} else {
			fmt.Fprintf(stdout, "  Semantic Candidates: %d (Reused: %d, Groups: %d)\n", result.SemanticCandidates, result.SemanticReused, result.SemanticGroups)
		}
		fmt.Fprintf(stdout, "  Corrective Candidates: %d (Reused: %d, Clusters: %d)\n", result.CorrectiveCandidates, result.CorrectiveReused, result.CorrectiveClusters)
		fmt.Fprintf(stdout, "  Mixed Binding Episodes: %d\n", result.MixedBindingEpisodes)
		fmt.Fprintf(stdout, "  Episodes Without Verification: %d\n", result.EpisodesWithoutVerification)
		if len(result.Failures) > 0 {
			fmt.Fprintf(stdout, "  Failures: %d\n", len(result.Failures))
			for _, f := range result.Failures {
				fmt.Fprintf(stdout, "    - [%s] %s: %s\n", f.Phase, f.Key, f.Error)
			}
		}
	}
	return exitOK
}
