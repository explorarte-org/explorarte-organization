package ceochat

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

type fixedOwnerResolver struct{ identity campaign.OwnerIdentity }

func (f fixedOwnerResolver) ResolveOwner(context.Context, string) (campaign.OwnerIdentity, error) {
	return f.identity, nil
}

// approveAsOwner creates an approval the only way one can be created: through the
// owner approver, which resolves the owner and binds the exact proposal and review.
// The CEO's tools cannot do it.
func approveAsOwner(t *testing.T, store campaign.Store, auth CapabilityAuthorizer, proposalID, reviewID int64) campaign.OwnerApprovalResult {
	t.Helper()
	svc := campaign.NewApprovalService(store, auth, permissiveExecutionRequirements())
	approver, err := campaign.NewOwnerApprover("org-test", store, svc,
		fixedOwnerResolver{identity: campaign.OwnerIdentity{RoleID: "owner", AuthorityClass: "owner", RuntimeKind: "human", OrganizationRevisionID: 1}}, auth, nil)
	if err != nil {
		t.Fatalf("NewOwnerApprover: %v", err)
	}
	result, err := approver.Approve(context.Background(), proposalID, reviewID)
	if err != nil {
		t.Fatalf("owner approval of proposal %d + review %d: %v", proposalID, reviewID, err)
	}
	return result
}
