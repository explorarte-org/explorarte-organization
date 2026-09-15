package campaign

import (
	"context"
	"fmt"
	"strings"
)

const (
	// CapabilityProposalRevise is required to create a new proposal revision.
	CapabilityProposalRevise = "campaign.proposal.revise"

	// CapabilityOwnerApprovalCreate is required to create an owner execution approval.
	CapabilityOwnerApprovalCreate = "campaign.owner_approval.create"

	// CapabilityOwnerApprovalRead is required to read owner execution approvals.
	CapabilityOwnerApprovalRead = "campaign.owner_approval.read"
)

// ApprovalService handles deterministic owner execution approval without LLM calls.
type ApprovalService struct {
	Store      Store
	Authorizer CapabilityAuthorizer
}

// NewApprovalService creates an ApprovalService.
func NewApprovalService(store Store, authorizer CapabilityAuthorizer) *ApprovalService {
	return &ApprovalService{Store: store, Authorizer: authorizer}
}

// ApproveParams contains the input parameters for creating an owner execution approval.
type ApproveParams struct {
	OrganizationID    string
	RevisionID        int64
	ProposalID        int64
	FinancialReviewID int64
	ApprovedByRoleID  string
	ConversationID    int64
	MessageID         int64
	TurnTaskID        int64
	ToolCallID        string
}

// ApproveForExecution creates a durable owner approval for the given proposal+review tuple.
// This is fully deterministic: no LLM calls.
//
// Invariants enforced:
// - review.ProposalID == proposal.ID
// - review.ProposalCanonicalHash == proposal.CanonicalHash
// - review.Verdict == "recommended"
// - review.RecommendedBudget != nil
// - approver has campaign.owner_approval.create capability
// - approver != finance reviewer (SoD)
func (s *ApprovalService) ApproveForExecution(ctx context.Context, params ApproveParams) (CampaignOwnerApproval, bool, error) {
	if strings.TrimSpace(params.OrganizationID) == "" {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: organization_id is required", ErrInvalidInput)
	}
	if params.ProposalID <= 0 {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: proposal_id must be positive", ErrInvalidInput)
	}
	if params.FinancialReviewID <= 0 {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: financial_review_id must be positive", ErrInvalidInput)
	}
	if strings.TrimSpace(params.ApprovedByRoleID) == "" {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: approved_by_role_id is required", ErrInvalidInput)
	}
	if strings.TrimSpace(params.ToolCallID) == "" {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: tool_call_id is required", ErrInvalidInput)
	}

	// 1. Authorize the caller.
	if s.Authorizer != nil {
		if err := s.Authorizer.Authorize(ctx, params.OrganizationID, params.RevisionID, params.ApprovedByRoleID, CapabilityOwnerApprovalCreate); err != nil {
			return CampaignOwnerApproval{}, false, fmt.Errorf("%w: actor %q lacks %s: %v", ErrUnauthorized, params.ApprovedByRoleID, CapabilityOwnerApprovalCreate, err)
		}
	}

	// 2. Load proposal.
	proposal, err := s.Store.GetProposal(ctx, params.OrganizationID, params.ProposalID)
	if err != nil {
		return CampaignOwnerApproval{}, false, fmt.Errorf("load proposal %d: %w", params.ProposalID, err)
	}
	if proposal.OrganizationID != params.OrganizationID {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: cross-org proposal access", ErrUnauthorized)
	}

	// 3. Load financial review.
	review, err := s.Store.GetFinancialReview(ctx, params.OrganizationID, params.FinancialReviewID)
	if err != nil {
		return CampaignOwnerApproval{}, false, fmt.Errorf("load financial review %d: %w", params.FinancialReviewID, err)
	}
	if review.OrganizationID != params.OrganizationID {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: cross-org review access", ErrUnauthorized)
	}

	// 4. Verify review pins the exact proposal.
	if review.ProposalID != proposal.ID {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: review proposal_id %d != proposal %d", ErrProposalHashMismatch, review.ProposalID, proposal.ID)
	}
	if review.ProposalCanonicalHash != proposal.CanonicalHash {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: review proposal hash %q != proposal hash %q", ErrProposalHashMismatch, review.ProposalCanonicalHash, proposal.CanonicalHash)
	}

	// 5. Verify verdict is recommended.
	if review.Verdict != VerdictRecommended {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: verdict is %q, only %q is approvable", ErrReviewNotRecommended, review.Verdict, VerdictRecommended)
	}

	// 6. Verify budget exists.
	if review.RecommendedBudget == nil {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: financial review has no recommended budget", ErrInvalidInput)
	}

	// 7. SoD: approver != finance reviewer.
	if params.ApprovedByRoleID == review.ReviewerRoleID {
		return CampaignOwnerApproval{}, false, fmt.Errorf("%w: approver %q is the finance reviewer", ErrSeparationOfDutiesViolation, params.ApprovedByRoleID)
	}

	// 8. Compute idempotency key.
	idempotencyKey := fmt.Sprintf("cappr:%d:%d:%s:%s:%s",
		params.ConversationID, params.TurnTaskID, params.ToolCallID,
		proposal.CanonicalHash, review.CanonicalHash)

	// 9. Freeze execution budget from review recommendation.
	executionBudget := *review.RecommendedBudget

	// 10. Compute canonical hash.
	canonicalHash, err := ComputeApprovalCanonicalHash(ApprovalCanonicalPayload{
		OrganizationID:               params.OrganizationID,
		ProposalID:                   proposal.ID,
		ProposalCanonicalHash:        proposal.CanonicalHash,
		FinancialReviewID:            review.ID,
		FinancialReviewCanonicalHash: review.CanonicalHash,
		ApprovedByRoleID:             params.ApprovedByRoleID,
		ExecutionBudget:              executionBudget,
	})
	if err != nil {
		return CampaignOwnerApproval{}, false, fmt.Errorf("compute approval hash: %w", err)
	}

	// 11. Persist.
	approval, reused, err := s.Store.CreateOwnerApproval(ctx, CreateOwnerApprovalCommand{
		OrganizationID:               params.OrganizationID,
		ProposalID:                   proposal.ID,
		ProposalCanonicalHash:        proposal.CanonicalHash,
		FinancialReviewID:            review.ID,
		FinancialReviewCanonicalHash: review.CanonicalHash,
		ApprovedByRoleID:             params.ApprovedByRoleID,
		ConversationID:               params.ConversationID,
		MessageID:                    params.MessageID,
		TurnTaskID:                   params.TurnTaskID,
		ToolCallID:                   params.ToolCallID,
		ExecutionBudget:              executionBudget,
		IdempotencyKey:               idempotencyKey,
		CanonicalHash:                canonicalHash,
	})
	if err != nil {
		return CampaignOwnerApproval{}, false, fmt.Errorf("persist approval: %w", err)
	}

	return approval, reused, nil
}
