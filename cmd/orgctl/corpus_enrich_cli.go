package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/corpusenrich"
)

// runCorpusEnrichAbstracts implements `orgctl corpus enrich-abstracts`.
// Reads (canonical_id, S2 paperId) pairs directly from the harvester's
// s2_id_cache table (already-resolved IDs, per owner instruction: "usar
// IDs existentes", never a new title search), fetches abstracts via the
// unauthenticated Semantic Scholar batch endpoint (confirmed live: 200,
// 100% coverage on a 20-ID sample, no key required), and writes a
// resumable, per-batch-checkpointed JSONL plus a canonical_id->paperId
// join file so a later step can attach an abstract to a Work by its own
// identity. Never touches internal/rag, never writes to the
// organization's Postgres.
func runCorpusEnrichAbstracts(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("corpus enrich-abstracts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	harvesterDB := flags.String("harvester-db", "", "path to the harvester's SQLite state DB (a snapshot, read-only)")
	stateFile := flags.String("state-file", "", "path to this command's own resumable abstract JSONL state file")
	batchSize := flags.Int("batch-size", 150, "paperIDs per Semantic Scholar batch request (max 500)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*harvesterDB) == "" || strings.TrimSpace(*stateFile) == "" {
		fmt.Fprintln(stderr, "--harvester-db and --state-file are required")
		return exitUsage
	}

	binary, err := exec.LookPath("sqlite3")
	if err != nil {
		fmt.Fprintf(stderr, "sqlite3 not found: %v\n", err)
		return exitInternal
	}
	cmd := exec.Command(binary, "-readonly", "-json", *harvesterDB,
		"SELECT paper_id AS canonical_id, response_json FROM s2_id_cache WHERE status='found';")
	rawOut, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(stderr, "query s2_id_cache: %v\n", err)
		return exitInternal
	}
	var rows []struct {
		CanonicalID  string `json:"canonical_id"`
		ResponseJSON string `json:"response_json"`
	}
	trimmed := strings.TrimSpace(string(rawOut))
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
			fmt.Fprintf(stderr, "decode s2_id_cache rows: %v\n", err)
			return exitInternal
		}
	}

	paperIDToCanonical := make(map[string]string, len(rows))
	var paperIDs []string
	seen := make(map[string]bool)
	for _, row := range rows {
		var parsed struct {
			PaperID string `json:"paperId"`
		}
		if err := json.Unmarshal([]byte(row.ResponseJSON), &parsed); err != nil || parsed.PaperID == "" {
			continue
		}
		paperIDToCanonical[parsed.PaperID] = row.CanonicalID
		if !seen[parsed.PaperID] {
			seen[parsed.PaperID] = true
			paperIDs = append(paperIDs, parsed.PaperID)
		}
	}

	store, err := corpusenrich.OpenStore(*stateFile)
	if err != nil {
		fmt.Fprintf(stderr, "open state store: %v\n", err)
		return exitInternal
	}
	client := corpusenrich.NewClient(20 * time.Second)
	orchestrator := &corpusenrich.Orchestrator{
		Client: client, Store: store,
		Config: func() corpusenrich.OrchestratorConfig {
			cfg := corpusenrich.DefaultOrchestratorConfig()
			cfg.BatchSize = *batchSize
			return cfg
		}(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	result, err := orchestrator.Run(ctx, paperIDs)
	if err != nil {
		fmt.Fprintf(stderr, "enrichment run: %v\n", err)
		return exitInternal
	}

	writeValue(stdout, *jsonOutput, struct {
		UniquePaperIDs int                    `json:"unique_paper_ids"`
		Result         corpusenrich.RunResult `json:"result"`
	}{len(paperIDs), result})
	return exitOK
}
