package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"io"
	"os"
)

func runProgram(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "budget" {
		fmt.Fprintln(stderr, "usage: orgctl program budget <attach|show> ...")
		return exitUsage
	}
	cfg, service, cleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	switch args[1] {
	case "attach":
		if len(args) != 4 || args[2] == "" || args[3] == "" {
			fmt.Fprintln(stderr, "usage: orgctl program budget attach ROOT_TASK_ID --file POLICY.json")
			return exitUsage
		}
		var root int64
		if _, err := fmt.Sscan(args[2], &root); err != nil || root <= 0 {
			return exitUsage
		}
		raw, err := os.ReadFile(args[3])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		var p programbudget.Policy
		if err = json.Unmarshal(raw, &p); err != nil || p.ProgramRootTaskID != root || p.SchemaVersion != programbudget.SchemaVersion || p.Validate() != nil {
			fmt.Fprintln(stderr, "invalid program budget policy")
			return exitUsage
		}
		var meta map[string]any
		if err = json.Unmarshal(raw, &meta); err != nil {
			return exitInternal
		}
		_, err = service.RecordEvidence(ctx, tasks.RecordEvidenceCommand{TaskID: root, Type: tasks.RequirementResult, Reference: fmt.Sprintf("program-model-budget://%d", root), RecordedBy: "operator", Metadata: meta})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInternal
		}
		writeValue(stdout, true, map[string]any{"root_task_id": root, "schema": programbudget.SchemaVersion})
		return exitOK
	case "show":
		if len(args) != 3 {
			return exitUsage
		}
		var root int64
		if _, err := fmt.Sscan(args[2], &root); err != nil {
			return exitUsage
		}
		p, err := (programbudget.Resolver{Tasks: service}).Policy(ctx, root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInternal
		}
		if p.SchemaVersion == "" {
			fmt.Fprintln(stderr, "program budget policy not found")
			return exitInternal
		}
		writeValue(stdout, true, p)
		return exitOK
	default:
		return exitUsage
	}
}
