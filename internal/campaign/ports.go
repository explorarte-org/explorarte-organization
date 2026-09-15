package campaign

import "context"

// Store defines the durable persistence operations for campaign proposals and financial reviews.
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

	// RecordFinancialReview idempotently records an immutable financial review.
	RecordFinancialReview(ctx context.Context, cmd RecordFinancialReviewCommand) (review CampaignFinancialReview, reused bool, err error)

	// GetFinancialReview retrieves a financial review by ID.
	GetFinancialReview(ctx context.Context, organizationID string, id int64) (CampaignFinancialReview, error)

	// GetFinancialReviewByRequestID retrieves a financial review by its review request ID.
	GetFinancialReviewByRequestID(ctx context.Context, organizationID string, requestID int64) (CampaignFinancialReview, error)

	// GetLatestFinancialReviewForProposal retrieves the latest completed financial review for a proposal.
	GetLatestFinancialReviewForProposal(ctx context.Context, organizationID string, proposalID int64) (CampaignFinancialReview, error)
}
