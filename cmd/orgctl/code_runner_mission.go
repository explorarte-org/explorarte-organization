package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

// This operator surface calls the same mission service as the integration
// tests. It cannot invent a gate result, apply a promotion, push Git or approve
// implicitly. Production authorization remains inside that service/staging.
type missionReviewService interface {
	RequestPromotion(context.Context, int64, int64, string) (staging.Promotion, error)
	ReviewMission(context.Context, int64, int64, string, engineeringmission.Verdict, string, string) (staging.Promotion, error)
}

type missionReviewRequest struct {
	operation, actor, verdict, reasonCode, reason string
	task, workspace, promotion, requirement       int64
	jsonOutput                                    bool
}

func parseMissionReview(args []string, stderr io.Writer) (missionReviewRequest, error) {
	var r missionReviewRequest
	if len(args) == 0 || (args[0] != "request-review" && args[0] != "review") {
		return r, fmt.Errorf("usage: orgctl code-runner mission <request-review|review>; no implicit approval or apply")
	}
	r.operation = args[0]
	f := flag.NewFlagSet("code-runner mission "+r.operation, flag.ContinueOnError)
	f.SetOutput(stderr)
	f.StringVar(&r.actor, "actor-role", "", "explicit authorized actor role")
	f.BoolVar(&r.jsonOutput, "json", false, "emit JSON")
	if r.operation == "request-review" {
		f.Int64Var(&r.task, "task", 0, "CodeRunner mission task ID")
		f.Int64Var(&r.workspace, "workspace", 0, "sealed candidate workspace ID")
	} else {
		f.Int64Var(&r.promotion, "promotion", 0, "promotion ID")
		f.Int64Var(&r.requirement, "requirement", 0, "mission review approval requirement ID")
		f.StringVar(&r.verdict, "verdict", "", "APPROVE, REMEDIATE or BLOCK (no default)")
		f.StringVar(&r.reasonCode, "reason-code", "", "review reason code")
		f.StringVar(&r.reason, "reason", "", "independent review rationale")
	}
	if err := parseInterspersed(f, args[1:]); err != nil {
		return r, err
	}
	if f.NArg() != 0 || strings.TrimSpace(r.actor) == "" {
		return r, fmt.Errorf("explicit actor role required; positional arguments are not accepted")
	}
	if r.operation == "request-review" {
		if r.task <= 0 || r.workspace <= 0 {
			return r, fmt.Errorf("positive task and workspace IDs required")
		}
	} else {
		if r.promotion <= 0 || r.requirement <= 0 || strings.TrimSpace(r.reasonCode) == "" || strings.TrimSpace(r.reason) == "" {
			return r, fmt.Errorf("positive promotion and requirement IDs and an explicit review rationale required")
		}
		switch engineeringmission.Verdict(r.verdict) {
		case engineeringmission.Approve, engineeringmission.Remediate, engineeringmission.Block:
		default:
			return r, fmt.Errorf("verdict must be APPROVE, REMEDIATE or BLOCK")
		}
	}
	return r, nil
}

func executeMissionReview(ctx context.Context, s missionReviewService, r missionReviewRequest) (staging.Promotion, error) {
	switch r.operation {
	case "request-review":
		return s.RequestPromotion(ctx, r.task, r.workspace, r.actor)
	case "review":
		return s.ReviewMission(ctx, r.promotion, r.requirement, r.actor, engineeringmission.Verdict(r.verdict), r.reasonCode, r.reason)
	default:
		return staging.Promotion{}, fmt.Errorf("unsupported mission operation")
	}
}

func runCodeRunnerMission(args []string, stdout, stderr io.Writer) int {
	r, err := parseMissionReview(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	cfg, runtime, cleanup, code := openStagingRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	_, tasks, taskCleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer taskCleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Staging.CommandTimeout)
	defer cancel()
	value, err := executeMissionReview(ctx, engineeringmission.Service{Tasks: tasks, Promotion: runtime.Service}, r)
	if err != nil {
		return stagingError(stderr, err)
	}
	writeValue(stdout, r.jsonOutput, value)
	return exitOK
}
