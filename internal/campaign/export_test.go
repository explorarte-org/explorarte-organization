package campaign

import "context"

// ApproveUngrantedForTest runs the approval rules without an OwnerApprovalGrant.
// It exists only in test builds of this package: production code has exactly one
// way to create an approval, and it needs a grant.
func (s *ApprovalService) ApproveUngrantedForTest(ctx context.Context, params ApproveParams) (CampaignOwnerApproval, bool, error) {
	return s.approve(ctx, params, nil)
}
