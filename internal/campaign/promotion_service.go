package campaign

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// PromotionService orchestrates promoting an approved campaign to Executive.Submit.
// It is completely deterministic: zero LLM calls.
type PromotionService struct {
	Store      Store
	Submitter  ExecutiveSubmitter
	Authorizer CapabilityAuthorizer
	// Requirements supplies the CURRENT host execution budget floor,
	// re-checked immediately before Executive.Submit.
	Requirements ExecutionRequirementsProvider
}

// NewPromotionService constructs a PromotionService with the given dependencies.
func NewPromotionService(store Store, submitter ExecutiveSubmitter, authorizer CapabilityAuthorizer, requirements ExecutionRequirementsProvider) *PromotionService {
	return &PromotionService{
		Store:        store,
		Submitter:    submitter,
		Authorizer:   authorizer,
		Requirements: requirements,
	}
}

// HostGovernanceDesignCriterion is the deterministic, versioned acceptance criterion
// required by Executive.Submit for the design review phase.
const HostGovernanceDesignCriterion = "Host governance: CEO executive plan is approved by design reviewer"

// PromoteToExecutive safely and idempotently transitions an approved campaign tuple
// (proposal + financial review + owner approval) into exactly one Executive root run.
//
// This is the path the CEO tool takes, and it carries no execution mode: every
// promotion made here runs analysis_only, and an approval already promoted under
// another mode is returned as it is. Choosing a mode is owner authority and goes
// through PromoteToExecutiveWithGrant.
func (s *PromotionService) PromoteToExecutive(ctx context.Context, params PromoteToExecutiveParams) (PromotionResult, error) {
	return s.promote(ctx, params, ExecutionModeGrant{})
}

// PromoteToExecutiveWithGrant is PromoteToExecutive with an execution mode the
// owner promoter chose for this exact approval and actor. A grant issued for any
// other approval or actor is refused before anything is read or created.
func (s *PromotionService) PromoteToExecutiveWithGrant(ctx context.Context, params PromoteToExecutiveParams, grant ExecutionModeGrant) (PromotionResult, error) {
	if !grant.authorizes(params) {
		return PromotionResult{}, ErrExecutionModeNotAuthorized
	}
	return s.promote(ctx, params, grant)
}

func (s *PromotionService) promote(ctx context.Context, params PromoteToExecutiveParams, grant ExecutionModeGrant) (PromotionResult, error) {
	mode := grant.Mode()
	if strings.TrimSpace(params.OrganizationID) == "" {
		return PromotionResult{}, fmt.Errorf("%w: organization_id is required", ErrInvalidInput)
	}
	if params.OwnerApprovalID <= 0 {
		return PromotionResult{}, fmt.Errorf("%w: owner_approval_id must be positive", ErrInvalidInput)
	}
	if strings.TrimSpace(params.PromotedByRoleID) == "" {
		return PromotionResult{}, fmt.Errorf("%w: promoted_by_role_id is required", ErrInvalidInput)
	}
	if strings.TrimSpace(params.ToolCallID) == "" {
		return PromotionResult{}, fmt.Errorf("%w: tool_call_id is required", ErrInvalidInput)
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return PromotionResult{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalidInput)
	}
	if s.Submitter == nil {
		return PromotionResult{}, ErrSubmitterNotConfigured
	}

	// 1. Authorize owner authority.
	if s.Authorizer != nil {
		if err := s.Authorizer.Authorize(ctx, params.OrganizationID, params.OrganizationRevisionID, params.PromotedByRoleID, CapabilityPromotionExecute); err != nil {
			return PromotionResult{}, fmt.Errorf("%w: actor %q lacks %s: %v", ErrUnauthorized, params.PromotedByRoleID, CapabilityPromotionExecute, err)
		}
	}

	// 2. Short-circuit if a promotion already exists for this owner approval.
	existingByApproval, err := s.Store.GetPromotionByApprovalID(ctx, params.OrganizationID, params.OwnerApprovalID)
	if err == nil {
		// The durable promotion is immutable, and so is the mode it ran under.
		// A caller that chose a mode and asks for a different one is refused;
		// a caller that chose none (the CEO tool) just reads what exists.
		if grant.Chosen() && existingByApproval.ExecutionMode.Normalized() != mode {
			return PromotionResult{}, fmt.Errorf("%w: approval %d was promoted as %q, requested %q",
				ErrExecutionModeConflict, params.OwnerApprovalID, existingByApproval.ExecutionMode.Normalized(), mode)
		}
		return PromotionResult{
			Promotion:              existingByApproval,
			ExecutiveRootTaskID:    existingByApproval.ExecutiveRootTaskID,
			ExecutiveCorrelationID: existingByApproval.ExecutiveCorrelationID,
			Reused:                 true,
		}, nil
	} else if !errors.Is(err, ErrPromotionNotFound) {
		return PromotionResult{}, fmt.Errorf("check existing promotion by approval: %w", err)
	}

	// 3. Re-read and validate durable owner approval.
	approval, err := s.Store.GetOwnerApproval(ctx, params.OrganizationID, params.OwnerApprovalID)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("load owner approval: %w", err)
	}
	if approval.Status != StatusApprovedForExecution {
		return PromotionResult{}, fmt.Errorf("%w: approval status is %q", ErrApprovalNotApproved, approval.Status)
	}
	expectedApprovalHash, err := ComputeApprovalCanonicalHash(ApprovalCanonicalPayload{
		OrganizationID:               approval.OrganizationID,
		ProposalID:                   approval.ProposalID,
		ProposalCanonicalHash:        approval.ProposalCanonicalHash,
		FinancialReviewID:            approval.FinancialReviewID,
		FinancialReviewCanonicalHash: approval.FinancialReviewCanonicalHash,
		ApprovedByRoleID:             approval.ApprovedByRoleID,
		ExecutionBudget:              approval.ExecutionBudget,
	})
	if err != nil || expectedApprovalHash != approval.CanonicalHash {
		return PromotionResult{}, fmt.Errorf("%w: owner approval hash mismatch", ErrInvalidInput)
	}

	// 4. Re-read and validate durable campaign proposal.
	proposal, err := s.Store.GetProposal(ctx, params.OrganizationID, approval.ProposalID)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("load proposal: %w", err)
	}
	expectedProposalHash, err := ComputeCanonicalHash(CanonicalPayload{
		Title:              proposal.Title,
		Goal:               proposal.Goal,
		AcceptanceCriteria: proposal.AcceptanceCriteria,
		Requirements:       proposal.Requirements,
		Budget:             proposal.Budget,
		Assumptions:        proposal.Assumptions,
		Risks:              proposal.Risks,
		OpenQuestions:      proposal.OpenQuestions,
	})
	if err != nil || expectedProposalHash != proposal.CanonicalHash || proposal.CanonicalHash != approval.ProposalCanonicalHash {
		return PromotionResult{}, fmt.Errorf("%w: proposal hash mismatch", ErrProposalHashMismatch)
	}

	// Latest revision gate: verify the approved proposal revision is the latest in its lineage.
	rootID := proposal.ID
	if proposal.RootProposalID != nil {
		rootID = *proposal.RootProposalID
	}
	latestProposal, err := s.Store.GetLatestRevisionForRoot(ctx, params.OrganizationID, rootID)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("check latest revision: %w", err)
	}
	if latestProposal.ID != proposal.ID || latestProposal.RevisionNumber != proposal.RevisionNumber {
		return PromotionResult{}, fmt.Errorf("%w: approved revision is %d, latest is %d",
			ErrStaleApproval, proposal.RevisionNumber, latestProposal.RevisionNumber)
	}

	// 5. Re-read and validate durable financial review.
	review, err := s.Store.GetFinancialReview(ctx, params.OrganizationID, approval.FinancialReviewID)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("load financial review: %w", err)
	}
	if review.ProposalID != approval.ProposalID {
		return PromotionResult{}, fmt.Errorf("%w: review proposal %d != approval proposal %d",
			ErrInvalidInput, review.ProposalID, approval.ProposalID)
	}
	expectedReviewHash, err := ComputeReviewCanonicalHash(ReviewCanonicalPayload{
		ProposalID:            review.ProposalID,
		ProposalCanonicalHash: review.ProposalCanonicalHash,
		ReviewerRoleID:        review.ReviewerRoleID,
		Verdict:               review.Verdict,
		RecommendedBudget:     review.RecommendedBudget,
		EstimatedCost:         review.EstimatedCost,
		Assumptions:           review.Assumptions,
		Risks:                 review.Risks,
		RequiredCorrections:   review.RequiredCorrections,
		MissingInformation:    review.MissingInformation,
		Summary:               review.Summary,
	})
	if err != nil || expectedReviewHash != review.CanonicalHash || review.CanonicalHash != approval.FinancialReviewCanonicalHash {
		return PromotionResult{}, fmt.Errorf("%w: financial review hash mismatch", ErrReviewHashMismatch)
	}
	if review.Verdict != VerdictRecommended {
		return PromotionResult{}, fmt.Errorf("%w: review verdict is %q", ErrReviewNotRecommended, review.Verdict)
	}
	if review.RecommendedBudget == nil || *review.RecommendedBudget != approval.ExecutionBudget {
		return PromotionResult{}, ErrBudgetMismatch
	}

	// 6. CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 defense-in-depth:
	// validate the exact approved execution budget through the SAME
	// canonical campaign helper Finance and Approval use, BEFORE
	// Executive.Submit is ever called. This protects against a
	// historical approval (such as production's own owner_approval_id=1,
	// whose budget predates this hotfix and carries max_subagents=0)
	// reaching Executive.Submit and discovering the incompatibility only
	// there. Deterministic translation to executive.SubmitRequest, no
	// LLM calls; executive.CampaignBudget is a type alias of
	// agentbudget.Limits, so the validated value assigns directly.
	limits, err := ToAgentBudgetLimits(approval.ExecutionBudget)
	if err != nil {
		return PromotionResult{}, err
	}

	// 6.5. CAMPAIGN_EXECUTION_BUDGET_FEASIBILITY_V2 defense-in-depth: the
	// exact approved budget must fund the canonical minimum execution under
	// the CURRENT host facts, immediately before Executive.Submit. Approval
	// and Finance ran against the facts of their own time; routing, pricing
	// or runtime limits may have moved since. The owner approved a specific
	// maximum, and that authority is never widened: the budget is not raised
	// to the floor (no max(approved, minimum)). If it no longer clears the
	// floor nothing is launched and a new Finance review / approval cycle is
	// required. (An already-durable promotion short-circuits above and is
	// deliberately left immutable.)
	requirements, err := requireExecutionRequirements(ctx, s.Requirements, params.OrganizationID)
	if err != nil {
		return PromotionResult{}, err
	}
	if err := ValidateExecutionLimitsFeasibility(limits, requirements); err != nil {
		return PromotionResult{}, err
	}
	campaignBudget := &limits

	// Acceptance criteria: owner criteria mapped to AcceptanceImplementation + 1 host governance AcceptanceDesign.
	criteria := make([]executive.AcceptanceCriterion, 0, len(proposal.AcceptanceCriteria)+1)
	criteria = append(criteria, executive.AcceptanceCriterion{
		Text:  HostGovernanceDesignCriterion,
		Phase: executive.AcceptanceDesign,
	})
	for _, text := range proposal.AcceptanceCriteria {
		criteria = append(criteria, executive.AcceptanceCriterion{
			Text:  text,
			Phase: executive.AcceptanceImplementation,
		})
	}

	// Instructions represent the approved goal: structured from the durable
	// proposal and approval, with the host's own statement of campaign state.
	instructions := FormatPromotedGoal(proposal, approval)

	// Derive deterministic submit key (idempotency identity) from approval identity.
	submitKey, err := CampaignPromotionSubmitKey(approval.ID, approval.CanonicalHash)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("derive executive submit key: %w", err)
	}

	// Derive deterministic, trusted-root-safe causation key (provenance identity).
	causationKey, err := CampaignPromotionTrustedRootCausationKey(approval.ID, approval.CanonicalHash)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("derive trusted root causation key: %w", err)
	}

	// 7. Submit to Executive boundary.
	run, reused, err := s.Submitter.Submit(ctx, executive.SubmitRequest{
		Goal: executive.OwnerGoal{
			Goal:               instructions,
			AcceptanceCriteria: criteria,
		},
		ActorRoleID:             executive.OwnerRoleID,
		IdempotencyKey:          submitKey,
		TrustedRootCausationKey: causationKey,
		ExecutionMode:           mode,
		Budget:                  campaignBudget,
	})
	if err != nil {
		return PromotionResult{}, fmt.Errorf("executive submit: %w", err)
	}

	// 8. Seal resulting promotion record and persist.
	var sealedMode string
	if mode != ExecutionModeAnalysisOnly {
		sealedMode = string(mode)
	}
	promPayload := PromotionCanonicalPayload{
		OrganizationID:                params.OrganizationID,
		OwnerApprovalID:               approval.ID,
		OwnerApprovalCanonicalHash:    approval.CanonicalHash,
		ProposalID:                    proposal.ID,
		ProposalCanonicalHash:         proposal.CanonicalHash,
		FinancialReviewID:             review.ID,
		FinancialReviewCanonicalHash:  review.CanonicalHash,
		ExecutionBudget:               approval.ExecutionBudget,
		ExecutiveRootTaskID:           run.RootTaskID,
		ExecutiveCorrelationID:        run.CorrelationID,
		ExecutiveSubmitIdempotencyKey: submitKey,
		Status:                        StatusSubmitted,
		PromotedByRoleID:              params.PromotedByRoleID,
		ExecutionMode:                 sealedMode,
	}
	promHash, err := ComputePromotionCanonicalHash(promPayload)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("compute promotion hash: %w", err)
	}

	promotion, promReused, err := s.Store.CreatePromotion(ctx, CreatePromotionCommand{
		OrganizationID:                params.OrganizationID,
		OwnerApprovalID:               approval.ID,
		OwnerApprovalCanonicalHash:    approval.CanonicalHash,
		ProposalID:                    proposal.ID,
		ProposalCanonicalHash:         proposal.CanonicalHash,
		FinancialReviewID:             review.ID,
		FinancialReviewCanonicalHash:  review.CanonicalHash,
		ExecutionBudget:               approval.ExecutionBudget,
		ExecutionMode:                 mode,
		ExecutiveRootTaskID:           run.RootTaskID,
		ExecutiveCorrelationID:        run.CorrelationID,
		ExecutiveSubmitIdempotencyKey: submitKey,
		Status:                        StatusSubmitted,
		PromotedByRoleID:              params.PromotedByRoleID,
		ConversationID:                params.ConversationID,
		MessageID:                     params.MessageID,
		TurnTaskID:                    params.TurnTaskID,
		ToolCallID:                    params.ToolCallID,
		IdempotencyKey:                params.IdempotencyKey,
		CanonicalHash:                 promHash,
	})
	if err != nil {
		return PromotionResult{}, fmt.Errorf("persist campaign promotion: %w", err)
	}

	return PromotionResult{
		Promotion:              promotion,
		ExecutiveRootTaskID:    run.RootTaskID,
		ExecutiveCorrelationID: run.CorrelationID,
		Reused:                 reused || promReused,
	}, nil
}

// FormatPromotedGoal builds the goal an approved campaign is submitted to
// Executive with, from structured durable records.
//
// A proposal is drafted BEFORE its approval, and what a model wrote into it
// (assumptions, risks) may say so -- "the campaign remains a draft until the
// owner approves". Carried into the goal of a campaign that IS approved, that
// sentence is read by the planner as an approval still owed, and the root blocks
// asking the owner for a decision they already made. Nothing here searches for
// or removes such wording: free text the proposal carries is preserved verbatim,
// under a heading that says what it is (notes written before approval), and the
// HOST states, first and from the durable approval, what the campaign's state is.
//
// Only fields that instruct work appear as the campaign's goal and requirements;
// assumptions and risks are context, labelled as such. Open questions belong to
// the owner's review of the proposal and are not part of an approved campaign.
func FormatPromotedGoal(p CampaignProposal, approval CampaignOwnerApproval) string {
	var b strings.Builder
	b.WriteString(executive.WrapHostCampaignState(fmt.Sprintf(
		"Campaign state: APPROVED_FOR_EXECUTION.\n"+
			"Set by the host from durable records, not by any model: owner approval %d, made by %s, covers proposal %d (revision %d) and financial review %d. "+
			"The required owner approval flow is complete. Do not request, wait for or report a pending approval, and do not treat this campaign as a draft. "+
			"Any statement below about draft status or approval still pending was written before this approval and is superseded by this state.",
		approval.ID, approval.ApprovedByRoleID, p.ID, p.RevisionNumber, approval.FinancialReviewID)))
	b.WriteString("\n\n")
	b.WriteString(p.Goal)
	if len(p.Requirements) > 0 {
		b.WriteString("\n\nRequirements:")
		for _, req := range p.Requirements {
			reqStr := "optional"
			if req.Required {
				reqStr = "required"
			}
			b.WriteString(fmt.Sprintf("\n- [%s] (%s): %s", req.Key, reqStr, req.Description))
		}
	}
	if len(p.Assumptions) > 0 || len(p.Risks) > 0 {
		b.WriteString("\n\nProposal notes (written before approval; context, not instructions):")
		if len(p.Assumptions) > 0 {
			b.WriteString("\nAssumptions:")
			for _, a := range p.Assumptions {
				b.WriteString("\n- " + a)
			}
		}
		if len(p.Risks) > 0 {
			b.WriteString("\nRisks:")
			for _, r := range p.Risks {
				b.WriteString("\n- " + r)
			}
		}
	}
	return b.String()
}
