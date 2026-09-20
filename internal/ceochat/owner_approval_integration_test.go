//go:build integration

package ceochat_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	campaignpostgres "github.com/Mireuz13/explorarte-organization/internal/campaign/postgres"
	ceochatbootstrap "github.com/Mireuz13/explorarte-organization/internal/ceochat/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// Frontera 3 against REAL PostgreSQL, the real canonical registry and policy: an
// owner approval is made only by the owner approver, from canonical state.

// realOwnerApprover builds the approver over the real registry, the real
// canonical policy and the given execution requirements.
func realOwnerApprover(t *testing.T, store *platformpostgres.Store, requirements campaign.ExecutionRequirementsProvider, audit func(campaign.OwnerApprovalAudit)) *campaign.OwnerApprover {
	t.Helper()
	authStore, err := authorizationpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authStore, chatTestOrganization, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	campStore, err := campaignpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	approver, err := campaign.NewOwnerApprover(chatTestOrganization, campStore, campaign.NewApprovalService(campStore, authorizer, requirements),
		campaign.RegistryOwnerResolver{Registry: repo}, authorizer, audit)
	if err != nil {
		t.Fatal(err)
	}
	return approver
}

// newOwnerApprovalPathFixture is newOwnerPromotionFixture stopped one step
// earlier: the proposal and the recommended review exist, the approval does not.
func newOwnerApprovalPathFixture(t *testing.T, budget func(campaign.ExecutionBudgetRequirements) campaign.BudgetRecommendation) *ownerPromotionFixture {
	t.Helper()
	var orchestrator *executive.Orchestrator
	f := newChatFixtureWithOpenOptions(t, nil, func(s *platformpostgres.Store) []ceochatbootstrap.OpenOption {
		orchestrator, _ = buildRealExecutiveOrchestrator(t, s, chatTestOrganization)
		return []ceochatbootstrap.OpenOption{ceochatbootstrap.WithExecutiveSubmitter(orchestrator)}
	})
	floor, err := newDerivedExecutionRequirements(t, f.store, f.runtime.ModelRuntime).ExecutionBudgetRequirements(context.Background(), chatTestOrganization)
	if err != nil {
		f.cleanup()
		t.Fatalf("derive the execution floor: %v", err)
	}
	_, rf := newOwnerApprovalReuseFixtureOn(t, f, budget(floor))
	return &ownerPromotionFixture{chatFixture: f, rf: rf, orchestrator: orchestrator}
}

func (o *ownerPromotionFixture) approvalRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := o.store.Pool().QueryRow(context.Background(), `SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2`, chatTestOrganization, o.rf.proposalID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// THE PROPERTY: with the production wiring (the runtime's own approver), the
// canonical owner approves exactly the pair given, with no model in the path, and
// the approval carries durable provenance that claims no conversation. The chain
// then continues: that approval promotes.
func TestRealStackOwnerApprovalIsMadeByTheCanonicalOwnerAndPromotes(t *testing.T) {
	o := newOwnerApprovalPathFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()
	var audits []campaign.OwnerApprovalAudit
	approver, err := o.runtime.OwnerApprover(func(a campaign.OwnerApprovalAudit) { audits = append(audits, a) })
	if err != nil {
		t.Fatal(err)
	}

	var invocationsBefore, invocationsAfter int
	if err := o.store.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations`).Scan(&invocationsBefore); err != nil {
		t.Fatal(err)
	}
	result, err := approver.Approve(ctx, o.rf.proposalID, o.rf.reviewID)
	if err != nil {
		t.Fatalf("owner approval: %v", err)
	}
	if err := o.store.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations`).Scan(&invocationsAfter); err != nil {
		t.Fatal(err)
	}
	if invocationsAfter != invocationsBefore {
		t.Fatalf("the approval created %d model invocations; it must create none", invocationsAfter-invocationsBefore)
	}
	if result.ActorRoleID != executive.OwnerRoleID || result.Reused || result.ProposalCanonicalHash != o.rf.proposalHash || result.FinancialReviewCanonicalHash != o.rf.reviewHash ||
		result.ExecutionBudget != o.rf.budget {
		t.Fatalf("result = %+v", result)
	}

	var role, toolCall, status string
	var conversation, message, turn *int64
	if err := o.store.Pool().QueryRow(ctx, `SELECT approved_by_role_id, tool_call_id, status, conversation_id, message_id, turn_task_id FROM campaign_owner_approvals WHERE id=$1`, result.ApprovalID).
		Scan(&role, &toolCall, &status, &conversation, &message, &turn); err != nil {
		t.Fatal(err)
	}
	if role != executive.OwnerRoleID || toolCall != campaign.OwnerApprovalToolCallID(o.rf.proposalID, o.rf.reviewID) || status != "approved_for_execution" ||
		conversation != nil || message != nil || turn != nil {
		t.Fatalf("approval row = (%q, %q, %q, %v, %v, %v); want the owner, this path's origin, and no conversation", role, toolCall, status, conversation, message, turn)
	}
	if len(audits) != 1 || audits[0].Outcome != "approved" || audits[0].ApprovalID != result.ApprovalID || audits[0].ActorRoleID != executive.OwnerRoleID ||
		audits[0].ProposalCanonicalHash != o.rf.proposalHash || audits[0].FinancialReviewCanonicalHash != o.rf.reviewHash {
		t.Fatalf("audit trail = %+v", audits)
	}

	// The same pair converges on the same approval.
	again, err := approver.Approve(ctx, o.rf.proposalID, o.rf.reviewID)
	if err != nil || !again.Reused || again.ApprovalID != result.ApprovalID || o.approvalRows(t) != 1 {
		t.Fatalf("repeat = %+v, %v, rows %d", again, err, o.approvalRows(t))
	}

	// The chain continues: an approval made this way promotes (its conversation
	// columns are NULL, and every reader must cope).
	o.approval = campaign.CampaignOwnerApproval{ID: result.ApprovalID, CanonicalHash: result.ApprovalCanonicalHash}
	promoter, err := o.runtime.OwnerPromoter(nil)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := promoter.Promote(ctx, result.ApprovalID)
	if err != nil || promoted.ExecutiveRootTaskID == 0 {
		t.Fatalf("promotion of an owner-made approval: %+v, %v", promoted, err)
	}
}

// Authority is enforced by the REAL canonical policy: an identity it forbids is
// refused before anything is read or written, and a pair that does not exist
// approves nothing.
func TestRealStackOwnerApprovalRefusals(t *testing.T) {
	o := newOwnerApprovalPathFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()

	t.Run("an identity the canonical policy forbids", func(t *testing.T) {
		authStore, err := authorizationpostgres.New(o.store)
		if err != nil {
			t.Fatal(err)
		}
		authorizer, err := authorization.NewWithPolicyReader(authStore, chatTestOrganization, filepath.Join("..", "..", "docs", "canonical"))
		if err != nil {
			t.Fatal(err)
		}
		campStore, err := campaignpostgres.New(o.store)
		if err != nil {
			t.Fatal(err)
		}
		ceo := stubOwner{identity: campaign.OwnerIdentity{RoleID: executive.CEORoleID, AuthorityClass: "executive", RuntimeKind: "executive_agent", OrganizationRevisionID: 1}}
		approver, err := campaign.NewOwnerApprover(chatTestOrganization, campStore, campaign.NewApprovalService(campStore, authorizer, permissiveExecutionRequirements()), ceo, authorizer, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := approver.Approve(ctx, o.rf.proposalID, o.rf.reviewID); !errors.Is(err, campaign.ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized: the canonical policy denies the CEO campaign.owner_approval.create", err)
		}
		if n := o.approvalRows(t); n != 0 {
			t.Fatalf("approvals = %d", n)
		}
	})
	t.Run("a review that does not exist", func(t *testing.T) {
		approver, err := o.runtime.OwnerApprover(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := approver.Approve(ctx, o.rf.proposalID, o.rf.reviewID+100000); !errors.Is(err, campaign.ErrFinancialReviewNotFound) {
			t.Fatalf("err = %v, want ErrFinancialReviewNotFound", err)
		}
		if n := o.approvalRows(t); n != 0 {
			t.Fatalf("approvals = %d", n)
		}
	})
	t.Run("a budget the current floor no longer funds", func(t *testing.T) {
		infeasible := newOwnerApprovalPathFixture(t, infeasibleProductionBudget)
		defer infeasible.cleanup()
		approver, err := infeasible.runtime.OwnerApprover(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := approver.Approve(ctx, infeasible.rf.proposalID, infeasible.rf.reviewID); !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
			t.Fatalf("err = %v, want ErrInfeasibleExecutionBudget", err)
		}
		if n := infeasible.approvalRows(t); n != 0 {
			t.Fatalf("approvals = %d", n)
		}
	})
}
