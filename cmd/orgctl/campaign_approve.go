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
)

// campaignApproveDeadline bounds an approval: it reads canonical state and
// records one durable approval. No model is called.
const campaignApproveDeadline = 2 * time.Minute

func printCampaignApproveUsage(out io.Writer) {
	fmt.Fprintln(out, `  approve --proposal ID --review ID [--json]
      Approve a campaign for execution as the canonical owner, without a language
      model in the path. Approving is the owner's act and only this command (or
      another owner channel built on the same service) can do it: the CEO cannot.
      The owner is resolved from canonical state and the budget comes from the
      recommended financial review; neither can be supplied. The proposal and the
      review are bound exactly, by identity and canonical hash, and both must be
      the CURRENT ones. Running it again for the same pair reports the approval
      that already exists.`)
}

func runCampaignApprove(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("campaign approve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// Exactly three flags. Authority, budget and hashes are not parameters.
	proposalID := flags.Int64("proposal", 0, "campaign proposal ID to approve")
	reviewID := flags.Int64("review", 0, "recommended financial review ID of that proposal")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := parseInterspersed(flags, args); err != nil || flags.NArg() != 0 || *proposalID <= 0 || *reviewID <= 0 {
		fmt.Fprintln(stderr, "usage: orgctl campaign approve --proposal ID --review ID [--json]")
		return exitUsage
	}
	_, runtime, store, ctx, cancel, code := openCeoChatRuntime(stderr, "campaign-approve", campaignApproveDeadline)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()

	approver, err := runtime.OwnerApprover(func(event campaign.OwnerApprovalAudit) { emitOwnerApprovalAudit(stderr, event) })
	if err != nil {
		fmt.Fprintf(stderr, "campaign approve: %v\n", err)
		return exitInternal
	}
	return executeOwnerApproval(ctx, approver, *proposalID, *reviewID, *jsonOutput, stdout, stderr)
}

// ownerApprover is the one method executeOwnerApproval needs (*campaign.OwnerApprover).
type ownerApprover interface {
	Approve(ctx context.Context, proposalID, reviewID int64) (campaign.OwnerApprovalResult, error)
}

func executeOwnerApproval(ctx context.Context, approver ownerApprover, proposalID, reviewID int64, jsonOutput bool, stdout, stderr io.Writer) int {
	result, err := approver.Approve(ctx, proposalID, reviewID)
	if err != nil {
		fmt.Fprintf(stderr, "campaign approve: %s: %v\n", campaign.OwnerApprovalErrorClass(err), err)
		return ownerApprovalExitCode(err)
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
		return exitOK
	}
	verb := "approved"
	if result.Reused {
		verb = "already approved (reused)"
	}
	fmt.Fprintf(stdout, "proposal %d + review %d %s by %s: approval %d\n  budget: %s\n  next: orgctl campaign promote --approval %d\n",
		result.ProposalID, result.FinancialReviewID, verb, result.ActorRoleID, result.ApprovalID,
		formatBudget(result.ExecutionBudget), result.ApprovalID)
	return exitOK
}

func formatBudget(b campaign.BudgetRecommendation) string {
	return fmt.Sprintf("$%.6f, %d tokens, %d model calls, depth %d, %d subagents, %d retries, %d ms",
		b.MaxUSD, b.MaxTokens, b.MaxModelCalls, b.MaxDepth, b.MaxSubagents, b.MaxRetries, b.MaxWallTimeMS)
}

// ownerApprovalExitCode maps the audit error class to the CLI's exit codes.
func ownerApprovalExitCode(err error) int {
	switch campaign.OwnerApprovalErrorClass(err) {
	case "":
		return exitOK
	case "unauthorized", "owner_identity_unavailable", "separation_of_duties":
		return exitDenied
	case "proposal_not_found", "financial_review_not_found", "tuple_mismatch", "content_changed", "review_not_recommended",
		"stale", "approval_conflict", "infeasible_execution_budget", "invalid_execution_budget", "invalid_input":
		return exitInvalid
	default:
		return exitInternal
	}
}

// emitOwnerApprovalAudit writes one structured audit line per attempt: who
// approved, which exact proposal and review (with their hashes) and which
// approval it produced. The durable record of a successful approval is the
// CampaignOwnerApproval row itself; this line adds the actor's authority class,
// the outcome and the error class, including for attempts that failed before
// anything was written.
func emitOwnerApprovalAudit(out io.Writer, event campaign.OwnerApprovalAudit) {
	logger := slog.New(slog.NewJSONHandler(out, nil))
	attrs := []any{
		"event", "campaign.owner_approval",
		"proposal_id", event.ProposalID,
		"proposal_canonical_hash", event.ProposalCanonicalHash,
		"financial_review_id", event.FinancialReviewID,
		"financial_review_canonical_hash", event.FinancialReviewCanonicalHash,
		"actor_role_id", event.ActorRoleID,
		"actor_authority_class", event.ActorAuthorityClass,
		"organization_revision_id", event.OrganizationRevisionID,
		"outcome", event.Outcome,
		"approval_id", event.ApprovalID,
	}
	if event.Outcome == "failed" {
		attrs = append(attrs, "error_class", event.ErrorClass, "error", event.Error)
		logger.Warn("campaign owner approval", attrs...)
		return
	}
	logger.Info("campaign owner approval", attrs...)
}
