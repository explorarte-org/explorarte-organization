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
}

// NewPromotionService constructs a PromotionService with the given dependencies.
func NewPromotionService(store Store, submitter ExecutiveSubmitter, authorizer CapabilityAuthorizer) *PromotionService {
	return &PromotionService{
		Store:      store,
		Submitter:  submitter,
		Authorizer: authorizer,
	}
}

// HostGovernanceDesignCriterion is the deterministic, versioned acceptance criterion
// required by Executive.Submit for the design review phase.
const HostGovernanceDesignCriterion = "Host governance: CEO executive plan is approved by design reviewer"

// PromoteToExecutive safely and idempotently transitions an approved campaign tuple
// (proposal + financial review + owner approval) into exactly one Executive root run.
func (s *PromotionService) PromoteToExecutive(ctx context.Context, params PromoteToExecutiveParams) (PromotionResult, error) {
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

	// Instructions represent the approved goal and preserve all requirements, assumptions, and risks.
	instructions := FormatProposalGoal(proposal)

	// Derive deterministic, trusted-root-safe submit key from approval identity.
	submitKey, err := campaignPromotionSubmitKey(approval.ID, approval.CanonicalHash)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("derive executive submit key: %w", err)
	}

	// 7. Submit to Executive boundary.
	run, reused, err := s.Submitter.Submit(ctx, executive.SubmitRequest{
		Goal: executive.OwnerGoal{
			Goal:               instructions,
			AcceptanceCriteria: criteria,
		},
		ActorRoleID:    executive.OwnerRoleID,
		IdempotencyKey: submitKey,
		Budget:         campaignBudget,
	})
	if err != nil {
		return PromotionResult{}, fmt.Errorf("executive submit: %w", err)
	}

	// 8. Seal resulting promotion record and persist.
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

// FormatProposalGoal deterministic translation from proposal into instructions.
// It preserves the approved goal, structured requirements, assumptions, and risks.
func FormatProposalGoal(p CampaignProposal) string {
	var b strings.Builder
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
	if len(p.Assumptions) > 0 {
		b.WriteString("\n\nAssumptions:")
		for _, a := range p.Assumptions {
			b.WriteString("\n- " + a)
		}
	}
	if len(p.Risks) > 0 {
		b.WriteString("\n\nRisks:")
		for _, r := range p.Risks {
			b.WriteString("\n- " + r)
		}
	}
	return b.String()
}
