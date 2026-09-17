package campaign

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// ExecutiveSubmitter is the narrow execution boundary port implemented by Executive Orchestrator.
type ExecutiveSubmitter interface {
	Submit(ctx context.Context, request executive.SubmitRequest) (executive.Run, bool, error)
}

// Store defines the durable persistence operations for campaign proposals, financial reviews,
// owner approvals, and promotions to Executive.
type Store interface {
	// CreateProposal idempotently creates a new draft proposal.
	CreateProposal(ctx context.Context, cmd CreateProposalCommand) (proposal CampaignProposal, reused bool, err error)

	// GetProposal retrieves a campaign proposal by ID and organization ID.
	GetProposal(ctx context.Context, organizationID string, id int64) (CampaignProposal, error)

	// ListProposals lists campaign proposals for an organization ordered newest first.
	ListProposals(ctx context.Context, organizationID string, limit, offset int) ([]CampaignProposal, error)

	// CreateReviewRequest idempotently creates a new review request for a proposal.
	CreateReviewRequest(ctx context.Context, cmd CreateReviewRequestCommand) (req CampaignFinancialReviewRequest, reused bool, err error)

	// GetReviewRequest retrieves a review request by ID.
	GetReviewRequest(ctx context.Context, organizationID string, id int64) (CampaignFinancialReviewRequest, error)

	// GetLatestReviewRequestForProposal retrieves the latest review request for a proposal.
	GetLatestReviewRequestForProposal(ctx context.Context, organizationID string, proposalID int64) (CampaignFinancialReviewRequest, error)

	// GetReviewRequestByTaskID resolves the review request owning a
	// campaign.financial_review Task Engine task -- the canonical
	// task-ID-to-review-request lookup the autonomous finance worker uses.
	GetReviewRequestByTaskID(ctx context.Context, organizationID string, taskID int64) (CampaignFinancialReviewRequest, error)

	// RecordFinancialReview idempotently records an immutable financial review.
	RecordFinancialReview(ctx context.Context, cmd RecordFinancialReviewCommand) (review CampaignFinancialReview, reused bool, err error)

	// GetFinancialReview retrieves a financial review by ID.
	GetFinancialReview(ctx context.Context, organizationID string, id int64) (CampaignFinancialReview, error)

	// GetFinancialReviewByRequestID retrieves a financial review by its review request ID.
	GetFinancialReviewByRequestID(ctx context.Context, organizationID string, requestID int64) (CampaignFinancialReview, error)

	// GetLatestFinancialReviewForProposal retrieves the latest completed financial review for a proposal.
	GetLatestFinancialReviewForProposal(ctx context.Context, organizationID string, proposalID int64) (CampaignFinancialReview, error)

	// CreateRevision idempotently creates a new proposal revision with lineage.
	CreateRevision(ctx context.Context, cmd CreateRevisionCommand) (CampaignProposal, bool, error)

	// GetLatestRevisionForRoot retrieves the latest revision for a proposal root lineage.
	GetLatestRevisionForRoot(ctx context.Context, organizationID string, rootProposalID int64) (CampaignProposal, error)

	// CreateOwnerApproval idempotently creates an owner execution approval.
	CreateOwnerApproval(ctx context.Context, cmd CreateOwnerApprovalCommand) (CampaignOwnerApproval, bool, error)

	// GetOwnerApproval retrieves an owner approval by ID.
	GetOwnerApproval(ctx context.Context, organizationID string, approvalID int64) (CampaignOwnerApproval, error)

	// GetOwnerApprovalByProposal retrieves the owner approval for a specific proposal.
	GetOwnerApprovalByProposal(ctx context.Context, organizationID string, proposalID int64) (CampaignOwnerApproval, error)

	// CreatePromotion idempotently creates a campaign promotion record.
	CreatePromotion(ctx context.Context, cmd CreatePromotionCommand) (CampaignPromotion, bool, error)

	// GetPromotion retrieves a campaign promotion by ID.
	GetPromotion(ctx context.Context, organizationID string, id int64) (CampaignPromotion, error)

	// GetPromotionByApprovalID retrieves a campaign promotion by owner approval ID.
	GetPromotionByApprovalID(ctx context.Context, organizationID string, approvalID int64) (CampaignPromotion, error)
}
