package campaign_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// floor is the canonical minimum execution's constraints as production
// measured them (test evidence, not policy): the first CEO-plan dispatch
// reserved $0.1602536 and needed 33,268 + 128,000 tokens, and the minimal tree
// is five children at depths 1,2,3,2,1.
func floor() campaign.ExecutionBudgetRequirements {
	return campaign.ExecutionBudgetRequirements{
		MinUSD: 160_253_600, MinTokens: 161_268, MinModelCalls: 5,
		MinWallTimeMS: 1, MinDepth: 3, MinRetries: 1, MinSubagents: 5,
	}
}

func fixed(r campaign.ExecutionBudgetRequirements) campaign.ExecutionRequirementsProvider {
	return campaign.FixedExecutionRequirements{Requirements: r}
}

// productionBudget is financial_review 3 / owner_approval 3's budget, verbatim.
func productionBudget() campaign.BudgetRecommendation {
	return campaign.BudgetRecommendation{MaxUSD: 0.05, MaxTokens: 10000, MaxModelCalls: 5, MaxWallTimeMS: 300000, MaxDepth: 2, MaxRetries: 1, MaxSubagents: 1}
}

// feasibleBudget clears floor() with room to spare.
func feasibleBudget() campaign.BudgetRecommendation {
	return campaign.BudgetRecommendation{MaxUSD: 1.25, MaxTokens: 400000, MaxModelCalls: 20, MaxWallTimeMS: 3600000, MaxDepth: 4, MaxRetries: 2, MaxSubagents: 8}
}

// seedApproved records a recommended review and an owner approval for budget,
// the way durable history would hold them (no service in the way).
func seedApproved(t *testing.T, store *memCampaignStore, proposal campaign.CampaignProposal, budget campaign.BudgetRecommendation, key string) (campaign.CampaignFinancialReview, campaign.CampaignOwnerApproval) {
	t.Helper()
	ctx := context.Background()
	payload := campaign.ReviewCanonicalPayload{
		ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash, ReviewerRoleID: "empresa/finanzas",
		Verdict: campaign.VerdictRecommended, RecommendedBudget: &budget, Summary: "historical " + key,
	}
	hash, err := campaign.ComputeReviewCanonicalHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	review, _, err := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID: "org-1", ReviewRequestID: int64(1000 + len(store.financialReviews)), ProposalID: proposal.ID,
		ProposalCanonicalHash: proposal.CanonicalHash, ReviewerRoleID: payload.ReviewerRoleID, Verdict: payload.Verdict,
		RecommendedBudget: &budget, Summary: payload.Summary, CanonicalHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	apprHash, err := campaign.ComputeApprovalCanonicalHash(campaign.ApprovalCanonicalPayload{
		OrganizationID: "org-1", ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash,
		FinancialReviewID: review.ID, FinancialReviewCanonicalHash: review.CanonicalHash,
		ApprovedByRoleID: "empresa/human", ExecutionBudget: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, _, err := store.CreateOwnerApproval(ctx, campaign.CreateOwnerApprovalCommand{
		OrganizationID: "org-1", ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash,
		FinancialReviewID: review.ID, FinancialReviewCanonicalHash: review.CanonicalHash, ApprovedByRoleID: "empresa/human",
		ExecutionBudget: budget, IdempotencyKey: "appr-" + key, CanonicalHash: apprHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return review, approval
}

func promote(svc *campaign.PromotionService, approvalID int64, key string) (campaign.PromotionResult, error) {
	return svc.PromoteToExecutive(context.Background(), campaign.PromoteToExecutiveParams{
		OrganizationID: "org-1", OwnerApprovalID: approvalID, PromotedByRoleID: "empresa/human",
		ConversationID: 1, ToolCallID: "call-" + key, IdempotencyKey: "prom-" + key,
	})
}

// --- Promotion ---------------------------------------------------------------

// PROMOTION DEFENSE + AUTHORITY EXACTNESS: the owner approved a specific
// maximum. At the floor it launches with EXACTLY that maximum; below it, nothing
// launches and nothing is raised.
func TestPromotionRevalidatesFeasibilityBeforeSubmit(t *testing.T) {
	store, submitter, svc, proposal, _, _ := setupPromotionFixture(t)
	_, approval := seedApproved(t, store, proposal, feasibleBudget(), "feasible")
	svc.Requirements = fixed(floor())

	result, err := promote(svc, approval.ID, "feasible")
	if err != nil {
		t.Fatalf("a budget above the floor must launch: %v", err)
	}
	if submitter.submitCalls != 1 {
		t.Fatalf("Executive.Submit calls = %d, want 1", submitter.submitCalls)
	}
	got := submitter.lastRequest.Budget
	want, _ := campaign.ToAgentBudgetLimits(approval.ExecutionBudget)
	if got == nil || *got != want {
		t.Fatalf("submitted budget = %+v, want the owner-approved %+v exactly", got, want)
	}
	if result.Promotion.ExecutionBudget != approval.ExecutionBudget {
		t.Fatalf("recorded promotion budget %+v drifted from the approval %+v", result.Promotion.ExecutionBudget, approval.ExecutionBudget)
	}
	// Both contracts on the one Executive.Submit: the feasibility gate above ran
	// first, and the submission keeps Campaign's historical idempotency identity
	// with the SEPARATE trusted-root-safe causation.
	if want := fmt.Sprintf("campaign-promotion:%d:%.16s", approval.ID, approval.CanonicalHash); submitter.lastRequest.IdempotencyKey != want {
		t.Fatalf("IdempotencyKey = %q, want the historical %q", submitter.lastRequest.IdempotencyKey, want)
	}
	if want := fmt.Sprintf("campaign-promotion-%d-%.16s", approval.ID, approval.CanonicalHash); submitter.lastRequest.TrustedRootCausationKey != want {
		t.Fatalf("TrustedRootCausationKey = %q, want the separate colon-free %q", submitter.lastRequest.TrustedRootCausationKey, want)
	}
}

// ROUTING / PRICING / RUNTIME-LIMIT DRIFT between owner approval and launch:
// the same durable approval, a higher floor now. Reject; never launch; never
// silently raise the approved budget; require a new review/approval cycle.
func TestPromotionRejectsAnApprovalTheFloorHasOutgrown(t *testing.T) {
	store, submitter, svc, proposal, _, _ := setupPromotionFixture(t)
	budget := feasibleBudget()
	_, approval := seedApproved(t, store, proposal, budget, "drift")

	// Floor A: fine.
	svc.Requirements = fixed(floor())
	// Floor B: pricing/routing moved so one dispatch now reserves more than the
	// owner's whole approved maximum.
	drifted := floor()
	drifted.MinUSD = 2_000_000_000 // $2.00 > the approved $1.25
	svc.Requirements = fixed(drifted)

	_, err := promote(svc, approval.ID, "drift")
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("a floor above the approved budget must reject with ErrInfeasibleExecutionBudget, got: %v", err)
	}
	if submitter.submitCalls != 0 {
		t.Fatalf("Executive.Submit calls = %d, want 0 (no launch, no new root)", submitter.submitCalls)
	}
	if len(store.promotions) != 0 {
		t.Fatalf("promotion rows = %d, want 0", len(store.promotions))
	}
	after, err := store.GetOwnerApproval(context.Background(), "org-1", approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExecutionBudget != budget {
		t.Fatalf("the owner-approved budget changed from %+v to %+v: it must never be silently raised", budget, after.ExecutionBudget)
	}
}

// HISTORICAL DEFENSE: production's own approval 3 (0.05 USD / 10,000 tokens /
// depth 2 / 1 subagent) is valid data but cannot fund the canonical execution.
// A NEW launch from it is refused under today's floor.
func TestPromotionRefusesANewLaunchFromTheHistoricalInfeasibleApproval(t *testing.T) {
	store, submitter, svc, proposal, _, _ := setupPromotionFixture(t)
	_, approval3 := seedApproved(t, store, proposal, productionBudget(), "approval-3")
	svc.Requirements = fixed(floor())

	_, err := promote(svc, approval3.ID, "approval-3")
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("historical approval 3 must be refused for a new launch, got: %v", err)
	}
	if errors.Is(err, campaign.ErrInvalidExecutionBudget) {
		t.Fatalf("approval 3 is representable; it must not be reported as invalid: %v", err)
	}
	if submitter.submitCalls != 0 || len(store.promotions) != 0 {
		t.Fatalf("submit calls %d, promotions %d; want 0, 0", submitter.submitCalls, len(store.promotions))
	}
}

// The distinction this round documents: an ALREADY-DURABLE promotion (a root
// that already exists, like production's promotion 2 -> root 788) is history.
// It short-circuits before any feasibility check and is never rewritten; the
// defense above applies to NEW launches only.
func TestAnAlreadyDurablePromotionStaysImmutableEvenIfInfeasible(t *testing.T) {
	store, submitter, svc, proposal, _, _ := setupPromotionFixture(t)
	review, approval3 := seedApproved(t, store, proposal, productionBudget(), "durable")
	svc.Requirements = fixed(floor())
	hash, err := campaign.ComputePromotionCanonicalHash(campaign.PromotionCanonicalPayload{
		OrganizationID: "org-1", OwnerApprovalID: approval3.ID, OwnerApprovalCanonicalHash: approval3.CanonicalHash,
		ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash, FinancialReviewID: review.ID,
		FinancialReviewCanonicalHash: review.CanonicalHash, ExecutionBudget: approval3.ExecutionBudget,
		ExecutiveRootTaskID: 788, ExecutiveCorrelationID: "executive:owner-goal", Status: campaign.StatusSubmitted, PromotedByRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	durable, _, err := store.CreatePromotion(context.Background(), campaign.CreatePromotionCommand{
		OrganizationID: "org-1", OwnerApprovalID: approval3.ID, OwnerApprovalCanonicalHash: approval3.CanonicalHash,
		ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash, FinancialReviewID: review.ID,
		FinancialReviewCanonicalHash: review.CanonicalHash, ExecutionBudget: approval3.ExecutionBudget,
		ExecutiveRootTaskID: 788, ExecutiveCorrelationID: "executive:owner-goal", Status: campaign.StatusSubmitted,
		PromotedByRoleID: "empresa/human", ConversationID: 1, ToolCallID: "c", IdempotencyKey: "durable", CanonicalHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := promote(svc, approval3.ID, "again")
	if err != nil {
		t.Fatalf("the durable promotion must still be returned untouched: %v", err)
	}
	if !result.Reused || result.Promotion.ID != durable.ID || result.ExecutiveRootTaskID != 788 {
		t.Fatalf("durable promotion not reused as-is: %+v", result)
	}
	if submitter.submitCalls != 0 {
		t.Fatalf("Executive.Submit calls = %d, want 0", submitter.submitCalls)
	}
}

func TestPromotionFailsClosedWithoutAFloor(t *testing.T) {
	store, submitter, svc, proposal, _, _ := setupPromotionFixture(t)
	_, approval := seedApproved(t, store, proposal, feasibleBudget(), "nofloor")
	for name, provider := range map[string]campaign.ExecutionRequirementsProvider{
		"no provider":       nil,
		"provider errors":   errProvider{errors.New("pricing table unreadable")},
		"zero requirements": fixed(campaign.ExecutionBudgetRequirements{}),
	} {
		svc.Requirements = provider
		if _, err := promote(svc, approval.ID, "nofloor-"+name); !errors.Is(err, campaign.ErrExecutionRequirementsUnavailable) {
			t.Errorf("%s: want ErrExecutionRequirementsUnavailable, got: %v", name, err)
		}
	}
	if submitter.submitCalls != 0 {
		t.Fatalf("Executive.Submit calls = %d, want 0", submitter.submitCalls)
	}
}

type errProvider struct{ err error }

func (e errProvider) ExecutionBudgetRequirements(context.Context, string) (campaign.ExecutionBudgetRequirements, error) {
	return campaign.ExecutionBudgetRequirements{}, e.err
}

// --- Approval ----------------------------------------------------------------

func approveParams(proposal campaign.CampaignProposal, review campaign.CampaignFinancialReview, call string) campaign.ApproveParams {
	return campaign.ApproveParams{
		OrganizationID: "org-1", RevisionID: 1, ProposalID: proposal.ID, FinancialReviewID: review.ID,
		ApprovedByRoleID: "empresa/human", ConversationID: 1, TurnTaskID: 1, ToolCallID: call,
	}
}

func TestApprovalRejectsARecommendedBudgetBelowTheFloor(t *testing.T) {
	store, svc, proposal, _ := setupRevisionApprovalFixture()
	review, _ := seedReviewOnly(t, store, proposal, productionBudget(), "prod")
	svc.Requirements = fixed(floor())

	_, _, err := svc.ApproveUngrantedForTest(context.Background(), approveParams(proposal, review, "call-a"))
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("approving the production budget must fail with ErrInfeasibleExecutionBudget, got: %v", err)
	}
	if len(store.approvals) != 0 {
		t.Fatalf("owner approval rows = %d, want 0", len(store.approvals))
	}
}

func TestApprovalCreatesTheExactRecommendedBudgetWhenFeasible(t *testing.T) {
	store, svc, proposal, _ := setupRevisionApprovalFixture()
	budget := feasibleBudget()
	review, _ := seedReviewOnly(t, store, proposal, budget, "ok")
	svc.Requirements = fixed(floor())

	approval, _, err := svc.ApproveUngrantedForTest(context.Background(), approveParams(proposal, review, "call-b"))
	if err != nil {
		t.Fatalf("a feasible recommended budget must be approvable: %v", err)
	}
	if approval.ExecutionBudget != budget {
		t.Fatalf("approval budget %+v is not the review's %+v exactly (no normalization, no raise to the floor)", approval.ExecutionBudget, budget)
	}
}

// DRIFT between Finance and approval: the review was fine when written, but the
// floor rose. Approval refuses; no owner approval exists to promote.
func TestApprovalRejectsAReviewTheFloorHasOutgrown(t *testing.T) {
	store, svc, proposal, _ := setupRevisionApprovalFixture()
	review, _ := seedReviewOnly(t, store, proposal, feasibleBudget(), "drift")
	drifted := floor()
	drifted.MinTokens = 5_000_000
	svc.Requirements = fixed(drifted)

	_, _, err := svc.ApproveUngrantedForTest(context.Background(), approveParams(proposal, review, "call-c"))
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("want ErrInfeasibleExecutionBudget, got: %v", err)
	}
	if len(store.approvals) != 0 {
		t.Fatalf("owner approval rows = %d, want 0", len(store.approvals))
	}
}

func TestApprovalFailsClosedWithoutAFloor(t *testing.T) {
	store, svc, proposal, _ := setupRevisionApprovalFixture()
	review, _ := seedReviewOnly(t, store, proposal, feasibleBudget(), "nofloor")
	for name, provider := range map[string]campaign.ExecutionRequirementsProvider{
		"no provider": nil, "provider errors": errProvider{errors.New("boom")}, "zero requirements": fixed(campaign.ExecutionBudgetRequirements{}),
	} {
		svc.Requirements = provider
		if _, _, err := svc.ApproveUngrantedForTest(context.Background(), approveParams(proposal, review, "call-"+name)); !errors.Is(err, campaign.ErrExecutionRequirementsUnavailable) {
			t.Errorf("%s: want ErrExecutionRequirementsUnavailable, got: %v", name, err)
		}
	}
	if len(store.approvals) != 0 {
		t.Fatalf("owner approval rows = %d, want 0", len(store.approvals))
	}
}

// seedReviewOnly records one more recommended review (no approval) for budget.
func seedReviewOnly(t *testing.T, store *memCampaignStore, proposal campaign.CampaignProposal, budget campaign.BudgetRecommendation, key string) (campaign.CampaignFinancialReview, struct{}) {
	t.Helper()
	payload := campaign.ReviewCanonicalPayload{
		ProposalID: proposal.ID, ProposalCanonicalHash: proposal.CanonicalHash, ReviewerRoleID: "empresa/finanzas",
		Verdict: campaign.VerdictRecommended, RecommendedBudget: &budget, Summary: "review " + key,
	}
	hash, err := campaign.ComputeReviewCanonicalHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	review, _, err := store.RecordFinancialReview(context.Background(), campaign.RecordFinancialReviewCommand{
		OrganizationID: "org-1", ReviewRequestID: int64(2000 + len(store.financialReviews)), ProposalID: proposal.ID,
		ProposalCanonicalHash: proposal.CanonicalHash, ReviewerRoleID: payload.ReviewerRoleID, Verdict: payload.Verdict,
		RecommendedBudget: &budget, Summary: payload.Summary, CanonicalHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return review, struct{}{}
}

// --- Finance -----------------------------------------------------------------

// financeServiceWithFloor builds a FinanceService over the deterministic
// fixture's store and task coordinator that enforces the given floor.
func financeServiceWithFloor(t *testing.T, provider campaign.ExecutionRequirementsProvider) (*memCampaignStore, *fakeTaskCoordinator, *campaign.FinanceService) {
	t.Helper()
	store, taskCoord, _, auth := setupDeterministicFixture(t)
	svc, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID: "org-test", Requirements: provider, Store: store, Tasks: taskCoord, Authorizer: auth,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, taskCoord, svc
}

func requestReview(t *testing.T, svc *campaign.FinanceService, store *memCampaignStore, call string) (campaign.CampaignFinancialReviewRequest, int64) {
	t.Helper()
	prop := createTestProposal(t, store, "org-test", "Campaign "+call, "Goal")
	req, task, _, err := svc.RequestReview(context.Background(), campaign.RequestReviewParams{
		OrganizationID: "org-test", ProposalID: prop.ID, RequestedByRoleID: "empresa/ceo", RequestedFromTaskID: 20, ToolCallID: call,
	})
	if err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	return req, task.ID
}

// The exact production recommendation, run through the real service gate: valid
// data, infeasible -- a distinct terminal classification, nothing persisted.
func TestFinanceRecordsBudgetInfeasibleForTheProductionRecommendation(t *testing.T) {
	store, taskCoord, svc := financeServiceWithFloor(t, fixed(floor()))
	req, taskID := requestReview(t, svc, store, "call_infeasible")
	budget := productionBudget()
	mock := campaign.FinanceReviewOutput{Verdict: string(campaign.VerdictRecommended), Summary: "production repro", RecommendedBudget: &budget}

	_, _, err := svc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: taskID, ReviewRequestID: req.ID, MockOutput: &mock,
	})
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("want ErrInfeasibleExecutionBudget, got: %v", err)
	}
	if got := taskCoord.lastRecordedResult.FailureCode; got != "FINANCE_BUDGET_INFEASIBLE" {
		t.Fatalf("failure code = %q, want FINANCE_BUDGET_INFEASIBLE (not FINANCE_OUTPUT_INVALID, not a provider failure)", got)
	}
	if got := taskCoord.lastRecordedResult.Outcome; got != tasks.OutcomeNonRetryableFailure {
		t.Fatalf("outcome = %q, want terminal non-retryable (never a blind retry)", got)
	}
	if len(store.financialReviews) != 0 {
		t.Fatalf("financial reviews persisted = %d, want 0", len(store.financialReviews))
	}
	if len(store.approvals) != 0 {
		t.Fatalf("owner approvals = %d, want 0", len(store.approvals))
	}
	taskCoord.mu.Lock()
	status := taskCoord.tasks[taskID].Status
	taskCoord.mu.Unlock()
	if status == tasks.StatusCompleted {
		t.Fatal("the task must not complete")
	}
}

// Malformed output keeps its own classification.
func TestFinanceKeepsOutputInvalidForNonRepresentableBudgets(t *testing.T) {
	store, taskCoord, svc := financeServiceWithFloor(t, fixed(floor()))
	req, taskID := requestReview(t, svc, store, "call_invalid")
	budget := productionBudget()
	budget.MaxSubagents = 0
	mock := campaign.FinanceReviewOutput{Verdict: string(campaign.VerdictRecommended), Summary: "zero", RecommendedBudget: &budget}
	_, _, err := svc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: taskID, ReviewRequestID: req.ID, MockOutput: &mock,
	})
	if !errors.Is(err, campaign.ErrInvalidExecutionBudget) || errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("want ErrInvalidExecutionBudget only, got: %v", err)
	}
	if got := taskCoord.lastRecordedResult.FailureCode; got != "FINANCE_OUTPUT_INVALID" {
		t.Fatalf("failure code = %q, want FINANCE_OUTPUT_INVALID", got)
	}
}

// A budget at or above the floor is recorded EXACTLY as recommended: no
// normalization, no clamping.
func TestFinancePersistsAFeasibleBudgetUnchanged(t *testing.T) {
	store, _, svc := financeServiceWithFloor(t, fixed(floor()))
	req, taskID := requestReview(t, svc, store, "call_feasible")
	budget := feasibleBudget()
	mock := campaign.FinanceReviewOutput{Verdict: string(campaign.VerdictRecommended), Summary: "ok", RecommendedBudget: &budget}
	review, _, err := svc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: taskID, ReviewRequestID: req.ID, MockOutput: &mock,
	})
	if err != nil {
		t.Fatalf("a feasible recommendation must be recorded: %v", err)
	}
	if review.RecommendedBudget == nil || *review.RecommendedBudget != budget {
		t.Fatalf("recorded budget %+v is not the recommended %+v", review.RecommendedBudget, budget)
	}
	if len(store.financialReviews) != 1 {
		t.Fatalf("financial reviews = %d, want 1", len(store.financialReviews))
	}
}

// Non-recommended verdicts are not gated by the floor.
func TestFinanceNonRecommendedVerdictsSkipTheFloor(t *testing.T) {
	store, _, svc := financeServiceWithFloor(t, fixed(floor()))
	req, taskID := requestReview(t, svc, store, "call_changes")
	mock := campaign.FinanceReviewOutput{Verdict: string(campaign.VerdictChangesRequested), Summary: "needs work"}
	if _, _, err := svc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: taskID, ReviewRequestID: req.ID, MockOutput: &mock,
	}); err != nil {
		t.Fatalf("changes_requested with no budget must be recorded: %v", err)
	}
}

// Without a way to derive the floor Finance neither claims a task nor spends: a
// nil provider is refused before anything is read or claimed.
func TestFinanceRefusesToExecuteWithoutAFloorProvider(t *testing.T) {
	store, taskCoord, svc := financeServiceWithFloor(t, nil)
	req, taskID := requestReview(t, svc, store, "call_nofloor")
	budget := feasibleBudget()
	mock := campaign.FinanceReviewOutput{Verdict: string(campaign.VerdictRecommended), Summary: "ok", RecommendedBudget: &budget}
	_, _, err := svc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: taskID, ReviewRequestID: req.ID, MockOutput: &mock,
	})
	if !errors.Is(err, campaign.ErrExecutionRequirementsUnavailable) {
		t.Fatalf("want ErrExecutionRequirementsUnavailable, got: %v", err)
	}
	taskCoord.mu.Lock()
	status := taskCoord.tasks[taskID].Status
	taskCoord.mu.Unlock()
	if status != tasks.StatusReady {
		t.Fatalf("a review with no floor provider must not claim the task: status = %q", status)
	}
	if len(store.financialReviews) != 0 {
		t.Fatal("no review may be persisted")
	}
}

// A provider that cannot derive the floor fails the attempt terminally BEFORE
// any model runs (here: before MockOutput is even consulted) and persists nothing.
func TestFinanceFailsTheAttemptWhenTheFloorCannotBeDerived(t *testing.T) {
	store, taskCoord, svc := financeServiceWithFloor(t, errProvider{errors.New("no priced route for the CEO")})
	req, taskID := requestReview(t, svc, store, "call_floor_err")
	budget := feasibleBudget()
	mock := campaign.FinanceReviewOutput{Verdict: string(campaign.VerdictRecommended), Summary: "ok", RecommendedBudget: &budget}
	_, _, err := svc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: taskID, ReviewRequestID: req.ID, MockOutput: &mock,
	})
	if !errors.Is(err, campaign.ErrExecutionRequirementsUnavailable) {
		t.Fatalf("want ErrExecutionRequirementsUnavailable, got: %v", err)
	}
	if got := taskCoord.lastRecordedResult.FailureCode; got != "FINANCE_REQUIREMENTS_UNAVAILABLE" {
		t.Fatalf("failure code = %q, want FINANCE_REQUIREMENTS_UNAVAILABLE", got)
	}
	if len(store.financialReviews) != 0 {
		t.Fatal("no review may be persisted")
	}
}
