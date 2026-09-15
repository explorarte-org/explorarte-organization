package campaign

import "context"

// Store defines the durable persistence operations for campaign proposals.
type Store interface {
	// CreateProposal idempotently creates a new draft proposal.
	// If the proposal already exists under the same idempotency key:
	// - If the canonical hash matches: returns the existing proposal with reused=true, err=nil.
	// - If the canonical hash differs: returns ErrIdempotencyConflict.
	CreateProposal(ctx context.Context, cmd CreateProposalCommand) (proposal CampaignProposal, reused bool, err error)

	// GetProposal retrieves a campaign proposal by ID and organization ID.
	GetProposal(ctx context.Context, organizationID string, id int64) (CampaignProposal, error)

	// ListProposals lists campaign proposals for an organization ordered newest first.
	ListProposals(ctx context.Context, organizationID string, limit, offset int) ([]CampaignProposal, error)
}
