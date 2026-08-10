package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/corpuscensus"
	"github.com/Mireuz13/explorarte-organization/internal/pdfingest/poppler"
)

// runCorpus is the entry point for `orgctl corpus <census>`. This
// subcommand deliberately never opens a database connection: it is the
// preprocessing stage that runs BEFORE anything touches
// internal/rag/internal/authorization -- census is not knowledge, it is
// a recommendation about what to ingest later, per a separate command
// (orgctl rag ingest-pdf) run per-Work after this report is reviewed.
func runCorpus(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCorpusUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "census":
		return runCorpusCensus(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printCorpusUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown corpus command %q\n", args[0])
		printCorpusUsage(stderr)
		return exitUsage
	}
}

func printCorpusUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: orgctl corpus census [options]

  census --harvester-db PATH --state-file PATH [--concurrency N]
         [--self-improving-seed-sha256 PATH] [--json]
      Reads a paper harvester's SQLite state DB (read-only), validates
      each downloaded, not-yet-terminal PDF via Poppler, deduplicates by
      scholarly identity (DOI > arXiv > ACL/OpenReview > normalized
      title+year > SHA-256), classifies topic/authority tier, and writes
      a resumable Silver JSONL state file. Never opens a database
      connection, never creates a knowledge candidate, never touches
      Object Storage. Prints a Census report.`)
}

func runCorpusCensus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("corpus census", flag.ContinueOnError)
	flags.SetOutput(stderr)
	harvesterDB := flags.String("harvester-db", "", "path to the harvester's SQLite state DB (a snapshot, read-only)")
	stateFile := flags.String("state-file", "", "path to this package's own resumable Silver JSONL state file")
	concurrency := flags.Int("concurrency", 0, "bounded PDF-validation concurrency (0 = auto, capped at 8)")
	seedSHAPath := flags.String("self-improving-seed-sha256", "", "optional path to a JSON array of SHA-256 hashes already in the separate self-improving-agents seed, for cross-corpus dedup")
	discoveryCatalogs := flags.Int("discovery-catalogs", 0, "count of discovery catalog directories (Awesome-list clones) seen alongside the harvester's papers table, for reporting only")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*harvesterDB) == "" || strings.TrimSpace(*stateFile) == "" {
		fmt.Fprintln(stderr, "--harvester-db and --state-file are required")
		return exitUsage
	}

	reader, err := corpuscensus.NewSQLiteCLIReader(*harvesterDB)
	if err != nil {
		fmt.Fprintf(stderr, "open harvester reader: %v\n", err)
		return exitInternal
	}
	procCfg, err := poppler.DefaultConfig()
	if err != nil {
		fmt.Fprintf(stderr, "poppler config: %v\n", err)
		return exitInternal
	}
	processor, err := poppler.New(procCfg)
	if err != nil {
		fmt.Fprintf(stderr, "poppler init: %v\n", err)
		return exitInternal
	}
	store, err := corpuscensus.OpenStateStore(*stateFile)
	if err != nil {
		fmt.Fprintf(stderr, "open state store: %v\n", err)
		return exitInternal
	}

	var seedSHA256 map[string]bool
	if strings.TrimSpace(*seedSHAPath) != "" {
		raw, err := os.ReadFile(*seedSHAPath)
		if err != nil {
			fmt.Fprintf(stderr, "read self-improving seed SHA-256 file: %v\n", err)
			return exitUsage
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			fmt.Fprintf(stderr, "decode self-improving seed SHA-256 file: %v\n", err)
			return exitUsage
		}
		seedSHA256 = make(map[string]bool, len(list))
		for _, sha := range list {
			seedSHA256[strings.ToLower(strings.TrimSpace(sha))] = true
		}
	}

	orchestrator := &corpuscensus.Orchestrator{
		Reader:    reader,
		Processor: processor,
		Store:     store,
		Config: corpuscensus.OrchestratorConfig{
			Concurrency:           *concurrency,
			Validation:            corpuscensus.DefaultValidationConfig(),
			SelfImprovingSHA256:   seedSHA256,
			DiscoveryCatalogCount: *discoveryCatalogs,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	census, err := orchestrator.Run(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "corpus census: %v\n", err)
		return exitInternal
	}

	writeValue(stdout, *jsonOutput, census)
	return exitOK
}
