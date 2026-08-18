package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"io"
	"os"
	"strings"
)

const programTargetRef = "refs/heads/v2/program-context-memory-001"

func validProgramTargetRef(ref string) bool {
	return ref == programTargetRef
}

func runProgram(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: orgctl program <budget|promotion> ...")
		return exitUsage
	}
	if args[0] == "promotion" {
		return runProgramPromotion(args[1:], stdout, stderr)
	}
	if args[0] != "budget" {
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

// runProgramPromotion is the only application surface allowed to apply an
// approved candidate. It is deliberately separate from engineeringmission,
// and its target is a single non-production program ref.
func runProgramPromotion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 5 || args[0] != "apply" || args[2] != "--actor-role" || strings.TrimSpace(args[3]) == "" {
		fmt.Fprintln(stderr, "usage: orgctl program promotion apply PROMOTION_ID --actor-role ROLE [--json]")
		return exitUsage
	}
	id, err := positiveID(args[1], "promotion")
	if err != nil || args[4] != "--json" {
		return exitUsage
	}
	cfg, runtime, cleanup, code := openStagingRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Staging.CommandTimeout)
	defer cancel()
	promotion, err := runtime.Service.GetPromotion(ctx, id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	workspace, err := runtime.Service.GetWorkspace(ctx, promotion.WorkspaceID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	if !validProgramTargetRef(promotion.TargetRef) {
		fmt.Fprintln(stderr, "program promotion target denied")
		return exitDenied
	}
	if promotion.Status != staging.PromotionApproved || workspace.Status != staging.WorkspaceSealed || promotion.CandidateCommit == "" || promotion.ExpectedBaseCommit != workspace.BaseCommit || workspace.ActorRoleID == args[3] || promotion.ApprovedByRoleID == nil || *promotion.ApprovedByRoleID == workspace.ActorRoleID {
		fmt.Fprintln(stderr, "program promotion preconditions denied")
		return exitDenied
	}
	result, err := runtime.Service.ApplyPromotion(ctx, staging.ApplyPromotionCommand{PromotionID: id, ActorRoleID: args[3]})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	writeValue(stdout, true, result)
	return exitOK
}
