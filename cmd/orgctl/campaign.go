package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// campaignPromoteDeadline bounds a promotion: it opens the Executive and chat
// runtimes, reads canonical state and submits one root. No model is called.
const campaignPromoteDeadline = 2 * time.Minute

func runCampaign(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCampaignUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "approve":
		return runCampaignApprove(args[1:], stdout, stderr)
	case "promote":
		return runCampaignPromote(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printCampaignUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown campaign command %q\n", args[0])
		printCampaignUsage(stderr)
		return exitUsage
	}
}

func printCampaignUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl campaign <command> [options]
commands:`)
	printCampaignApproveUsage(out)
	fmt.Fprintln(out, `  promote --approval ID [--execution-mode analysis_only|governed_implementation] [--json]
      Promote an APPROVED campaign as the canonical owner, without a language
      model in the path. The acting owner is resolved from canonical state; there
      is deliberately no --actor-role / --owner-role flag, and no way to name who
      is acting. The promotion goes through the same PromotionService as the CEO's
      campaign.promote_to_executive tool (feasibility gate, historical idempotency
      key, trusted-root causation, durable promotion record).
      --execution-mode is the owner's explicit choice of how the campaign may
      work. Omitted, a new promotion is analysis_only and an existing one is
      reported as it is. governed_implementation opts the campaign into the
      governed path (design freeze, engineering mission, code-runner evidence);
      it can be chosen only here, never through the CEO, the goal text or a
      requirement, and an approval already promoted under another mode is refused.`)
}

func runCampaignPromote(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("campaign promote", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// Who acts is not a parameter of this command; how the campaign may work is
	// the owner's explicit choice and is the only decision a flag carries.
	approvalID := flags.Int64("approval", 0, "owner approval ID to promote")
	modeFlag := flags.String("execution-mode", "", "analysis_only or governed_implementation (omit to choose none)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 0 || *approvalID <= 0 {
		fmt.Fprintln(stderr, "usage: orgctl campaign promote --approval ID [--execution-mode analysis_only|governed_implementation] [--json]")
		return exitUsage
	}
	var mode campaign.ExecutionMode
	modeChosen := false
	flags.Visit(func(f *flag.Flag) { modeChosen = modeChosen || f.Name == "execution-mode" })
	if modeChosen {
		parsed, err := executive.ParseExecutionMode(*modeFlag)
		if err != nil {
			fmt.Fprintf(stderr, "campaign promote: %v\n", err)
			return exitUsage
		}
		mode = parsed
	}
	cfg, runtime, store, ctx, cancel, code := openCeoChatRuntime(stderr, "campaign-promote", campaignPromoteDeadline)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()
	_ = cfg

	promoter, err := runtime.OwnerPromoter(func(event campaign.OwnerPromotionAudit) { emitOwnerPromotionAudit(stderr, event) })
	if err != nil {
		fmt.Fprintf(stderr, "campaign promote: %v\n", err)
		return exitInternal
	}
	return executeOwnerPromotion(ctx, promoter, *approvalID, mode, modeChosen, *jsonOutput, stdout, stderr)
}

// ownerPromoter is the one method executeOwnerPromotion needs (*campaign.OwnerPromoter).
type ownerPromoter interface {
	Promote(ctx context.Context, approvalID int64) (campaign.OwnerPromotionResult, error)
	PromoteWithMode(ctx context.Context, approvalID int64, mode campaign.ExecutionMode) (campaign.OwnerPromotionResult, error)
}

func executeOwnerPromotion(ctx context.Context, promoter ownerPromoter, approvalID int64, mode campaign.ExecutionMode, modeChosen, jsonOutput bool, stdout, stderr io.Writer) int {
	var result campaign.OwnerPromotionResult
	var err error
	if modeChosen {
		result, err = promoter.PromoteWithMode(ctx, approvalID, mode)
	} else {
		result, err = promoter.Promote(ctx, approvalID)
	}
	if err != nil {
		fmt.Fprintf(stderr, "campaign promote: %s: %v\n", campaign.OwnerPromotionErrorClass(err), err)
		return ownerPromotionExitCode(err)
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
		return exitOK
	}
	verb := "promoted"
	if result.Reused {
		verb = "already promoted (reused)"
	}
	fmt.Fprintf(stdout, "approval %d %s by %s: promotion %d, executive root task %d\n  execution mode:  %s\n  idempotency key: %s\n  root causation:  %s\n",
		result.ApprovalID, verb, result.ActorRoleID, result.PromotionID, result.ExecutiveRootTaskID,
		result.ExecutionMode, result.ExecutiveSubmitIdempotencyKey, result.TrustedRootCausation)
	return exitOK
}

// ownerPromotionExitCode maps the audit error class to the CLI's exit codes.
func ownerPromotionExitCode(err error) int {
	switch campaign.OwnerPromotionErrorClass(err) {
	case "":
		return exitOK
	case "unauthorized", "owner_identity_unavailable":
		return exitDenied
	case "approval_not_approved":
		return exitApprovalRequired
	case "approval_not_found", "infeasible_execution_budget", "invalid_execution_budget", "invalid_input", "idempotency_conflict", "execution_mode_conflict":
		return exitInvalid
	default:
		return exitInternal
	}
}

// emitOwnerPromotionAudit writes one structured audit line per attempt. The
// durable record of a successful promotion is the CampaignPromotion row
// itself (approval, promoting role, root task, idempotency key) and the root
// task's causation; this line adds the actor's authority class, the outcome and
// the error class, including for attempts that failed before anything was written.
func emitOwnerPromotionAudit(out io.Writer, event campaign.OwnerPromotionAudit) {
	logger := slog.New(slog.NewJSONHandler(out, nil))
	attrs := []any{
		"event", "campaign.owner_promotion",
		"approval_id", event.ApprovalID,
		"actor_role_id", event.ActorRoleID,
		"actor_authority_class", event.ActorAuthorityClass,
		"organization_revision_id", event.OrganizationRevisionID,
		"outcome", event.Outcome,
		"promotion_id", event.PromotionID,
		"execution_mode_requested", event.ExecutionModeRequested,
		"execution_mode", event.ExecutionMode,
		"executive_root_task_id", event.ExecutiveRootTaskID,
		"executive_submit_idempotency_key", event.IdempotencyKey,
		"trusted_root_causation", event.TrustedRootCausation,
		"promotion_idempotency_key", event.PromotionIdempotency,
	}
	if event.Outcome == "failed" {
		attrs = append(attrs, "error_class", event.ErrorClass, "error", event.Error)
		logger.Warn("campaign owner promotion", attrs...)
		return
	}
	logger.Info("campaign owner promotion", attrs...)
}
