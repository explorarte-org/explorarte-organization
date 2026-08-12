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

// runCorpusCurationDedup implements `orgctl corpuscuration dedup`: a thin
// CLI wrapper around
// internal/corpuscuration.CollapseDuplicateWorksInClusterWithIdentifiers,
// the fixed (abstract-present > title-verified > lexicographic)
// identity-collapse preflight.
//
// NOT YET WIRED into `orgctl corpus <subcommand>` dispatch (cmd/orgctl/
// corpus.go) -- that file has unrelated in-flight changes from other
// work in this tree and is intentionally left untouched here. This
// command is a standalone entry point today so a future driver revision
// (see scratchpad/corpus/canary15_driver_v2.py's collapse_duplicates(),
// which still reimplements the OLD pre-fix logic in Python) has a real
// Go implementation to call instead of maintaining its own copy. Wiring
// this into `orgctl corpus` dispatch and rewriting the Python driver to
// shell out to it is a follow-up, not done here.
//
// Input is a JSON file: {"work_ids": [...], "meta": {work_id:
// corpuscuration.WorkIdentity, ...}}. Output is the JSON-encoded
// corpuscuration.CollapseResult.
func runCorpusCurationDedup(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("corpuscuration dedup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputFile := flags.String("input-file", "", "path to a JSON file: {\"work_ids\": [...], \"meta\": {work_id: WorkIdentity}}")
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
		WorkIDs []string                               `json:"work_ids"`
		Meta    map[string]corpuscuration.WorkIdentity `json:"meta"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		fmt.Fprintf(stderr, "parse input file: %v\n", err)
		return exitInternal
	}

	result := corpuscuration.CollapseDuplicateWorksInClusterWithIdentifiers(input.WorkIDs, input.Meta)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return exitInternal
	}
	return exitOK
}
