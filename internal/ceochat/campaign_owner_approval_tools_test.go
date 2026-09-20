package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

// Frontera 3: the CEO cannot mint a CampaignOwnerApproval. It can prepare one --
// check the pair and hand the owner the exact command -- and that tool writes nothing.

func TestNoCEOToolCanCreateAnOwnerApproval(t *testing.T) {
	store := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{}}
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth); err != nil {
		t.Fatal(err)
	}
	if _, exists := reg.Lookup("campaign.approve_for_execution"); exists {
		t.Fatal("campaign.approve_for_execution is registered: a tool called approve must not merely advise, and must not approve")
	}
	prepare, exists := reg.Lookup("campaign.prepare_owner_approval")
	if !exists {
		t.Fatal("campaign.prepare_owner_approval is not registered")
	}
	if prepare.Access != AccessReadOnly || prepare.Effect != ToolEffectRead {
		t.Fatalf("prepare tool access=%v effect=%v, want read-only", prepare.Access, prepare.Effect)
	}
	// The tool configuration holds no approval service at all.
	for i := 0; i < reflect.TypeOf(CampaignToolsConfig{}).NumField(); i++ {
		if name := reflect.TypeOf(CampaignToolsConfig{}).Field(i).Name; strings.Contains(name, "Approval") {
			t.Fatalf("CampaignToolsConfig.%s: the CEO's tools must hold no way to create an approval", name)
		}
	}
}

func TestPrepareOwnerApprovalReturnsTheOwnersCommandAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.financial_review.read": true}}
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth); err != nil {
		t.Fatal(err)
	}
	executor := RegistryToolExecutor{Registry: reg}
	turn := WithTurnContext(ctx, TurnContext{OrganizationID: "org-test", OrganizationRevisionID: 1, ConversationID: 100, OwnerRoleID: "owner", OwnerMessageID: 200, TaskID: 300, AttemptID: 1, ActorRoleID: "owner"})
	identity := executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID, TaskID: 300, AttemptID: 1}

	pHash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "T", Goal: "G"})
	p, _, _ := store.CreateProposal(ctx, campaign.CreateProposalCommand{OrganizationID: "org-test", Title: "T", Goal: "G", IdempotencyKey: "k", CanonicalHash: pHash, CreatedByRoleID: "empresa/ceo"})
	other, _, _ := store.CreateProposal(ctx, campaign.CreateProposalCommand{OrganizationID: "org-test", Title: "T2", Goal: "G2", IdempotencyKey: "k2", CanonicalHash: strings.Repeat("c", 64), CreatedByRoleID: "empresa/ceo"})
	budget := campaign.BudgetRecommendation{MaxUSD: 3, MaxTokens: 1000, MaxModelCalls: 5, MaxWallTimeMS: 1000, MaxDepth: 3, MaxRetries: 1, MaxSubagents: 5}
	record := func(proposal campaign.CampaignProposal, verdict campaign.FinancialReviewVerdict, requestID int64) campaign.CampaignFinancialReview {
		hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash, ReviewerRoleID: "empresa/finanzas", Verdict: verdict, RecommendedBudget: &budget})
		review, _, err := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{OrganizationID: "org-test", ReviewRequestID: requestID, ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash, ReviewerRoleID: "empresa/finanzas", Verdict: verdict, RecommendedBudget: &budget, CanonicalHash: hash})
		if err != nil {
			t.Fatal(err)
		}
		return review
	}
	good := record(p, campaign.VerdictRecommended, 1)
	rejected := record(other, campaign.VerdictChangesRequested, 2)

	call := func(arguments string) (json.RawMessage, error) {
		result, err := executor.Execute(turn, identity, executionharness.ToolRequest{ToolName: "campaign.prepare_owner_approval", ToolCallID: "c-" + arguments, Arguments: json.RawMessage(arguments)})
		return result.Content, err
	}
	content, err := call(fmt.Sprintf(`{"proposal_id": %d, "financial_review_id": %d}`, p.ID, good.ID))
	if err != nil {
		t.Fatal(err)
	}
	var got PrepareOwnerApprovalProjection
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if !got.OwnerActionRequired || got.Command != fmt.Sprintf("orgctl campaign approve --proposal %d --review %d", p.ID, good.ID) ||
		got.ProposalCanonicalHash != p.CanonicalHash || got.FinancialReviewCanonicalHash != good.CanonicalHash || got.RecommendedBudget != budget {
		t.Fatalf("projection = %+v", got)
	}
	if len(store.approvals) != 0 {
		t.Fatalf("the prepare tool created %d approval(s)", len(store.approvals))
	}

	// A pair the owner could not approve is refused, so the CEO never hands over a command that would fail.
	if _, err := call(fmt.Sprintf(`{"proposal_id": %d, "financial_review_id": %d}`, p.ID, rejected.ID)); !errors.Is(err, campaign.ErrProposalHashMismatch) {
		t.Fatalf("review of another proposal: err = %v", err)
	}
	if _, err := call(fmt.Sprintf(`{"proposal_id": %d, "financial_review_id": %d}`, other.ID, rejected.ID)); !errors.Is(err, campaign.ErrReviewNotRecommended) {
		t.Fatalf("not recommended: err = %v", err)
	}
	// Nothing that steers authority can be passed: not an owner, a role, a budget or a hash.
	for _, extra := range []string{`"owner_role_id": "x"`, `"approved_by_role_id": "x"`, `"budget": {"max_usd": 9}`, `"proposal_canonical_hash": "aa"`} {
		if _, err := call(fmt.Sprintf(`{"proposal_id": %d, "financial_review_id": %d, %s}`, p.ID, good.ID, extra)); err == nil {
			t.Fatalf("argument %s was accepted", extra)
		}
	}
	if len(store.approvals) != 0 {
		t.Fatalf("approvals = %d after every call", len(store.approvals))
	}
}

// A model that still tries the old tool name gets nothing: no approval, no effect.
func TestAModelCallingTheRetiredApprovalToolCreatesNothing(t *testing.T) {
	store := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.owner_approval.create": true}}
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth); err != nil {
		t.Fatal(err)
	}
	turn := WithTurnContext(context.Background(), TurnContext{OrganizationID: "org-test", OrganizationRevisionID: 1, ConversationID: 100, OwnerRoleID: "owner", OwnerMessageID: 200, TaskID: 300, AttemptID: 1, ActorRoleID: "owner"})
	_, err := RegistryToolExecutor{Registry: reg}.Execute(turn, executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID, TaskID: 300, AttemptID: 1},
		executionharness.ToolRequest{ToolName: "campaign.approve_for_execution", ToolCallID: "c", Arguments: json.RawMessage(`{"proposal_id": 1, "financial_review_id": 1}`)})
	if err == nil {
		t.Fatal("the retired tool executed")
	}
	if len(store.approvals) != 0 {
		t.Fatalf("approvals = %d", len(store.approvals))
	}
}
