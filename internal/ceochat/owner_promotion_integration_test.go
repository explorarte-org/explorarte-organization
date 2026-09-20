//go:build integration

package ceochat_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	ceochatbootstrap "github.com/Mireuz13/explorarte-organization/internal/ceochat/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// CAMPAIGN_PROMOTION_OWNER_CLI_V1 against REAL PostgreSQL, the real canonical
// registry and policy, the real Task Engine and the real Executive
// orchestrator: the deterministic owner path resolves WHO is acting from
// canonical state and then runs the SAME PromotionService the CEO tool holds.

type ownerPromotionFixture struct {
	*chatFixture
	rf           *ownerApprovalReuseFixture
	approval     campaign.CampaignOwnerApproval
	orchestrator *executive.Orchestrator
	audits       []campaign.OwnerPromotionAudit
}

// newOwnerPromotionFixture opens the REAL chat runtime with a real Executive as
// its submitter (so its PromotionService is the production one, wired to the
// derived execution requirements) and approves a campaign whose recommended and
// approved budget is budget.
func newOwnerPromotionFixture(t *testing.T, budget func(campaign.ExecutionBudgetRequirements) campaign.BudgetRecommendation) *ownerPromotionFixture {
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
	approval, _, err := rf.store.CreateOwnerApproval(context.Background(), rf.approvalCmd(t, "owner-promotion-approval-"+t.Name(), "call-owner-promotion-approval", rf.budget))
	if err != nil {
		f.cleanup()
		t.Fatalf("create owner approval: %v", err)
	}
	return &ownerPromotionFixture{chatFixture: f, rf: rf, approval: approval, orchestrator: orchestrator}
}

func (o *ownerPromotionFixture) promoter(t *testing.T) *campaign.OwnerPromoter {
	t.Helper()
	promoter, err := o.runtime.OwnerPromoter(func(a campaign.OwnerPromotionAudit) { o.audits = append(o.audits, a) })
	if err != nil {
		t.Fatalf("build the owner promoter from the chat runtime: %v", err)
	}
	return promoter
}

func (o *ownerPromotionFixture) rootsAndPromotions(t *testing.T) (roots, promotions int) {
	t.Helper()
	ctx := context.Background()
	if err := o.store.Pool().QueryRow(ctx, `SELECT count(*) FROM tasks WHERE organization_id=$1 AND idempotency_key LIKE $2`,
		chatTestOrganization, fmt.Sprintf("campaign-promotion:%d:%%", o.approval.ID)).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if err := o.store.Pool().QueryRow(ctx, `SELECT count(*) FROM campaign_promotions WHERE owner_approval_id=$1`, o.approval.ID).Scan(&promotions); err != nil {
		t.Fatal(err)
	}
	return roots, promotions
}

func feasibleAboveFloor(floor campaign.ExecutionBudgetRequirements) campaign.BudgetRecommendation {
	return budgetAboveFloor(floor)
}

func infeasibleProductionBudget(campaign.ExecutionBudgetRequirements) campaign.BudgetRecommendation {
	return *productionInfeasibleBudget()
}

// The canonical owner comes from the registry, not from a flag.
func TestOwnerPromotionResolvesTheCanonicalOwnerFromTheRealRegistry(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	repo, err := registry.NewPostgresRepository(f.store)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := campaign.RegistryOwnerResolver{Registry: repo}.ResolveOwner(context.Background(), chatTestOrganization)
	if err != nil {
		t.Fatalf("resolve the owner from the real canonical registry: %v", err)
	}
	if owner.RoleID != executive.OwnerRoleID || owner.AuthorityClass != "owner" || owner.RuntimeKind != "human" || owner.OrganizationRevisionID <= 0 {
		t.Fatalf("owner = %+v", owner)
	}
}

// THE PROPERTY: an approved, authorized, feasible campaign is promoted with no
// language model anywhere in the path, through the SAME PromotionService the
// CEO tool holds, with the historical idempotency key and the separate
// trusted-root causation -- and re-running converges.
func TestOwnerPromotionPromotesWithNoModelThroughTheSharedPromotionService(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()

	var invocationsBefore, invocationsAfter int
	if err := o.store.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations`).Scan(&invocationsBefore); err != nil {
		t.Fatal(err)
	}
	result, err := o.promoter(t).Promote(ctx, o.approval.ID)
	if err != nil {
		t.Fatalf("owner promotion: %v", err)
	}
	// No language model is anywhere in this path: not one invocation was created.
	if err := o.store.Pool().QueryRow(ctx, `SELECT count(*) FROM model_invocations`).Scan(&invocationsAfter); err != nil {
		t.Fatal(err)
	}
	if invocationsAfter != invocationsBefore {
		t.Fatalf("the owner promotion created %d model invocations; it must create none", invocationsAfter-invocationsBefore)
	}
	wantKey, _ := campaign.CampaignPromotionSubmitKey(o.approval.ID, o.approval.CanonicalHash)
	wantCausation, _ := campaign.CampaignPromotionTrustedRootCausationKey(o.approval.ID, o.approval.CanonicalHash)
	if result.ActorRoleID != executive.OwnerRoleID || result.Reused || result.ExecutiveSubmitIdempotencyKey != wantKey || result.TrustedRootCausation != "owner:"+wantCausation {
		t.Fatalf("result = %+v", result)
	}

	var idempotencyKey, causation, class string
	if err := o.store.Pool().QueryRow(ctx, `SELECT idempotency_key, causation_id, task_class FROM tasks WHERE id=$1`, result.ExecutiveRootTaskID).Scan(&idempotencyKey, &causation, &class); err != nil {
		t.Fatal(err)
	}
	if idempotencyKey != wantKey || causation != "owner:"+wantCausation || class != executive.TaskClassOwnerGoal || strings.Contains(strings.TrimPrefix(causation, "owner:"), ":") {
		t.Fatalf("root identities = (%q, %q, %q), want (%q, %q, %q)", idempotencyKey, causation, class, wantKey, "owner:"+wantCausation, executive.TaskClassOwnerGoal)
	}
	var promotedBy, toolCall string
	var conversationID *int64
	if err := o.store.Pool().QueryRow(ctx, `SELECT promoted_by_role_id, tool_call_id, conversation_id FROM campaign_promotions WHERE id=$1`, result.PromotionID).Scan(&promotedBy, &toolCall, &conversationID); err != nil {
		t.Fatal(err)
	}
	if promotedBy != executive.OwnerRoleID || toolCall != fmt.Sprintf("owner-cli:campaign-promote:%d", o.approval.ID) || conversationID != nil {
		t.Fatalf("promotion row = (%q, %q, %v)", promotedBy, toolCall, conversationID)
	}
	// The approved budget is the root's budget, exactly.
	var maxUSD, maxTokens, maxCalls, maxSubagents int64
	if err := o.store.Pool().QueryRow(ctx, `SELECT max_usd_nanos, max_tokens, max_model_calls, max_subagents FROM agent_budgets WHERE task_id=$1 AND parent_budget_id IS NULL`, result.ExecutiveRootTaskID).Scan(&maxUSD, &maxTokens, &maxCalls, &maxSubagents); err != nil {
		t.Fatal(err)
	}
	want, _ := campaign.ToAgentBudgetLimits(o.approval.ExecutionBudget)
	if maxUSD != int64(want.MaxUSD) || maxTokens != want.MaxTokens || maxCalls != want.MaxModelCalls || maxSubagents != want.MaxSubagents {
		t.Fatalf("root budget (%d, %d, %d, %d) != approved %+v", maxUSD, maxTokens, maxCalls, maxSubagents, want)
	}
	// Re-running converges: same promotion, same root, still one of each.
	again, err := o.promoter(t).Promote(ctx, o.approval.ID)
	if err != nil || !again.Reused || again.PromotionID != result.PromotionID || again.ExecutiveRootTaskID != result.ExecutiveRootTaskID {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	// ...and a promotion made the way the CEO tool makes one is found too: the
	// runtime hands both paths the one PromotionService.
	viaTool, err := o.runtime.PromotionService().PromoteToExecutive(ctx, campaign.PromoteToExecutiveParams{
		OrganizationID: chatTestOrganization, OrganizationRevisionID: result.OrganizationRevisionID, OwnerApprovalID: o.approval.ID,
		PromotedByRoleID: executive.OwnerRoleID, ConversationID: o.rf.conversationID, MessageID: o.rf.messageID, TurnTaskID: o.rf.taskID,
		ToolCallID: "call-from-the-ceo-tool", IdempotencyKey: fmt.Sprintf("campaign-promotion:%s:%d", chatTestOrganization, o.approval.ID),
	})
	if err != nil || !viaTool.Reused || viaTool.ExecutiveRootTaskID != result.ExecutiveRootTaskID {
		t.Fatalf("CEO-tool-shaped call = %+v, %v; want the owner path's root %d reused", viaTool, err, result.ExecutiveRootTaskID)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 1 || promotions != 1 {
		t.Fatalf("roots %d, promotions %d; want 1, 1", roots, promotions)
	}
	if len(o.audits) != 2 || o.audits[0].Outcome != "promoted" || o.audits[1].Outcome != "reused" || o.audits[0].ActorRoleID != executive.OwnerRoleID {
		t.Fatalf("audit trail = %+v", o.audits)
	}
}

// The execution-feasibility gate (with the REAL derived requirements): the
// production smoke's budget is refused, no root is created, and the approved
// budget is untouched.
func TestOwnerPromotionRefusesTheProductionInfeasibleBudget(t *testing.T) {
	o := newOwnerPromotionFixture(t, infeasibleProductionBudget)
	defer o.cleanup()
	ctx := context.Background()

	if _, err := o.promoter(t).Promote(ctx, o.approval.ID); !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("want ErrInfeasibleExecutionBudget, got: %v", err)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 0 || promotions != 0 {
		t.Fatalf("roots %d, promotions %d; want 0, 0 (no root on a pre-submit failure)", roots, promotions)
	}
	stored, err := o.rf.store.GetOwnerApproval(ctx, chatTestOrganization, o.approval.ID)
	if err != nil || stored.ExecutionBudget != o.approval.ExecutionBudget {
		t.Fatalf("the approved budget changed: %+v, %v", stored.ExecutionBudget, err)
	}
	if len(o.audits) != 1 || o.audits[0].ErrorClass != "infeasible_execution_budget" {
		t.Fatalf("audit = %+v", o.audits)
	}
}

// Authority is enforced by the REAL canonical policy, not by anything the
// caller says: an identity that policy hard-denies (the CEO, an executive) is
// refused before Executive is reached.
func TestOwnerPromotionIsDeniedForAnActorTheCanonicalPolicyForbids(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()
	authStore, err := authorizationpostgres.New(o.store)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authStore, chatTestOrganization, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	ceo := stubOwner{identity: campaign.OwnerIdentity{RoleID: executive.CEORoleID, AuthorityClass: "executive", RuntimeKind: "executive_agent", OrganizationRevisionID: 1}}
	promoter, err := campaign.NewOwnerPromoter(chatTestOrganization, o.rf.store, o.runtime.PromotionService(), ceo, authorizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := promoter.Promote(ctx, o.approval.ID); !errors.Is(err, campaign.ErrUnauthorized) {
		t.Fatalf("the canonical policy hard-denies campaign.promotion.execute to executives; want ErrUnauthorized, got: %v", err)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 0 || promotions != 0 {
		t.Fatalf("roots %d, promotions %d; want 0, 0", roots, promotions)
	}
}

type stubOwner struct{ identity campaign.OwnerIdentity }

func (s stubOwner) ResolveOwner(context.Context, string) (campaign.OwnerIdentity, error) {
	return s.identity, nil
}

// Without execution requirements the promotion fails explicitly and creates nothing.
func TestOwnerPromotionFailsExplicitlyWhenRequirementsAreUnavailable(t *testing.T) {
	o := newOwnerPromotionFixture(t, feasibleAboveFloor)
	defer o.cleanup()
	ctx := context.Background()
	authStore, err := authorizationpostgres.New(o.store)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authStore, chatTestOrganization, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := registry.NewPostgresRepository(o.store)
	if err != nil {
		t.Fatal(err)
	}
	noRequirements := campaign.NewPromotionService(o.rf.store, o.orchestrator, authorizer, nil)
	promoter, err := campaign.NewOwnerPromoter(chatTestOrganization, o.rf.store, noRequirements, campaign.RegistryOwnerResolver{Registry: repo}, authorizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := promoter.Promote(ctx, o.approval.ID); !errors.Is(err, campaign.ErrExecutionRequirementsUnavailable) {
		t.Fatalf("want ErrExecutionRequirementsUnavailable, got: %v", err)
	}
	if roots, promotions := o.rootsAndPromotions(t); roots != 0 || promotions != 0 {
		t.Fatalf("roots %d, promotions %d; want 0, 0", roots, promotions)
	}
}
