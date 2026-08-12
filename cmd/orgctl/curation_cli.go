package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/corpuscuration"
)

// runCuration implements `orgctl curation <dedup|validate-output>`, a
// small standalone dispatcher deliberately kept out of cmd/orgctl/
// corpus.go (which has unrelated in-flight changes elsewhere in this
// tree) and out of cmd/orgctl/corpuscuration_dedup_cli.go (owned by a
// separate piece of work). This is the missing piece that finally lets
// an orchestrator (the canary driver) call the real, fixed Go logic for
// dedup (P0-E) and the semantic output contract (P0-C) instead of
// reimplementing either in Python.
func runCuration(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: orgctl curation <dedup|validate-output> [flags]")
		return exitUsage
	}
	switch args[0] {
	case "dedup":
		return runCorpusCurationDedup(args[1:], stdout, stderr)
	case "validate-output":
		return runCurationValidateOutput(args[1:], stdout, stderr)
	case "validate-adjudication":
		return runCurationValidateAdjudication(args[1:], stdout, stderr)
	case "validate-external-import":
		return runCurationValidateExternalImport(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown curation subcommand %q\n", args[0])
		return exitUsage
	}
}

// runCurationValidateOutput implements `orgctl curation validate-output`:
// a thin CLI wrapper around
// internal/corpuscuration.ValidateCurationOutputContract (P0-C/BUG 2 --
// a schema-valid curation response that silently drops or duplicates a
// Work must never be accepted as "succeeded").
//
// Input is a JSON file:
//
//	{
//	  "expected_cluster_id": "...",
//	  "expected_work_ids": ["...", ...],
//	  "output": {"cluster_id": "...", "works": [{"work_id": "...", "tier": "..."}]}
//	}
//
// On a valid contract, prints {"valid": true} and exits 0. On a
// violation, prints the JSON-encoded *corpuscuration.OutputContractViolation
// (classification always "curation_output_contract_invalid") to stdout
// and exits exitDrift (3), so a caller can distinguish "contract
// violated" from a usage/internal error without parsing stderr text.
func runCurationValidateOutput(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("curation validate-output", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputFile := flags.String("input-file", "", "path to a JSON file: {expected_cluster_id, expected_work_ids, output}")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*inputFile) == "" {
		fmt.Fprintln(stderr, "--input-file is required")
		return exitUsage
	}

	raw, err := os.ReadFile(*inputFile)
	if err != nil {
		fmt.Fprintf(stderr, "read input file: %v\n", err)
		return exitInternal
	}

	var input struct {
		ExpectedClusterID string                        `json:"expected_cluster_id"`
		ExpectedWorkIDs   []string                      `json:"expected_work_ids"`
		Output            corpuscuration.CurationOutput `json:"output"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		fmt.Fprintf(stderr, "parse input file: %v\n", err)
		return exitInternal
	}

	violation := corpuscuration.ValidateCurationOutputContract(input.ExpectedClusterID, input.ExpectedWorkIDs, input.Output)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if violation == nil {
		if err := enc.Encode(map[string]bool{"valid": true}); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	if err := enc.Encode(violation); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return exitInternal
	}
	return exitDrift
}

// runCurationValidateAdjudication implements `orgctl curation
// validate-adjudication`: a thin CLI wrapper around
// internal/corpuscuration.ValidateAdjudicationOutputContract (R10.3 Part
// B -- negative-tier adjudication). Mirrors runCurationValidateOutput
// exactly, adapted to the adjudication vocabulary.
//
// Input is a JSON file:
//
//	{
//	  "expected_cluster_id": "...",
//	  "candidate_work_ids": ["...", ...],
//	  "output": {"cluster_id": "...", "decisions": [{"work_id": "...", "decision": "KEEP|DISCARD|REVIEW"}]}
//	}
func runCurationValidateAdjudication(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("curation validate-adjudication", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputFile := flags.String("input-file", "", "path to a JSON file: {expected_cluster_id, candidate_work_ids, output}")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*inputFile) == "" {
		fmt.Fprintln(stderr, "--input-file is required")
		return exitUsage
	}

	raw, err := os.ReadFile(*inputFile)
	if err != nil {
		fmt.Fprintf(stderr, "read input file: %v\n", err)
		return exitInternal
	}

	var input struct {
		ExpectedClusterID string                            `json:"expected_cluster_id"`
		CandidateWorkIDs  []string                          `json:"candidate_work_ids"`
		Output            corpuscuration.AdjudicationOutput `json:"output"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		fmt.Fprintf(stderr, "parse input file: %v\n", err)
		return exitInternal
	}

	violation := corpuscuration.ValidateAdjudicationOutputContract(input.ExpectedClusterID, input.CandidateWorkIDs, input.Output)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if violation == nil {
		if err := enc.Encode(map[string]bool{"valid": true}); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	if err := enc.Encode(violation); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return exitInternal
	}
	return exitDrift
}

// runCurationValidateExternalImport implements `orgctl curation
// validate-external-import`: R10.5's quarantine-only import validator for
// external compute (Kaggle/local-model benchmark) results. Thin wrapper
// around corpuscuration.ValidateExternalResultBatch -- never writes to
// Knowledge/Silver tables, only validates and reports. Malformed JSON,
// missing/extra/duplicate/unknown work_ids, and invalid tiers are all
// rejected per entry, independent of every other entry in the batch.
//
// Input is a JSON file: an array of
//
//	{
//	  "cluster_id": "...", "expected_work_ids": ["...", ...],
//	  "terminal_valid": true,
//	  "output": {"cluster_id": "...", "works": [{"work_id": "...", "tier": "..."}]}
//	}
//
// Prints a JSON array of outcomes ({cluster_id, accepted, reason,
// violation?}) and exits exitDrift (3) if ANY entry was rejected, so a
// caller can distinguish "clean batch" from "at least one rejection"
// without parsing the full output.
func runCurationValidateExternalImport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("curation validate-external-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputFile := flags.String("input-file", "", "path to a JSON file: array of external result batch entries")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*inputFile) == "" {
		fmt.Fprintln(stderr, "--input-file is required")
		return exitUsage
	}

	raw, err := os.ReadFile(*inputFile)
	if err != nil {
		fmt.Fprintf(stderr, "read input file: %v\n", err)
		return exitInternal
	}

	var entries []struct {
		ClusterID       string                        `json:"cluster_id"`
		ExpectedWorkIDs []string                      `json:"expected_work_ids"`
		TerminalValid   bool                          `json:"terminal_valid"`
		Output          corpuscuration.CurationOutput `json:"output"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		fmt.Fprintf(stderr, "parse input file: %v\n", err)
		return exitInternal
	}

	batch := make([]corpuscuration.ExternalResultBatchEntry, 0, len(entries))
	for _, e := range entries {
		batch = append(batch, corpuscuration.ExternalResultBatchEntry{
			ClusterID: e.ClusterID, ExpectedWorkIDs: e.ExpectedWorkIDs,
			TerminalValid: e.TerminalValid, Output: e.Output,
		})
	}
	outcomes := corpuscuration.ValidateExternalResultBatch(batch)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(outcomes); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return exitInternal
	}

	for _, o := range outcomes {
		if !o.Accepted {
			return exitDrift
		}
	}
	return exitOK
}
