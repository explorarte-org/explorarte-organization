//go:build integration

package ceochat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// CAMPAIGN_EXECUTION_BUDGET_FEASIBILITY_V2 against the REAL Finance stack:
// real PostgreSQL, Task Engine, Authority, Context Engine, Harness and Model
// Runtime dispatching to test.fake (REAL_PROVIDER_CALLS=0). The host floor is
// injected (mutableRequirements) so the assertions are about the contract, not
// about today's prices; the derivation itself is proven in
// internal/campaign/executionrequirements and, against the real registry and
// rate card, by TestExecutionRequirementsDeriveFromTheRealCanonicalFacts.

// integrationFloor mirrors the canonical minimum execution as production
// measured it (test evidence, not policy): first CEO-plan reservation
// $0.1602536 = 33,268 input + 128,000 output tokens; five children at depths
// 1,2,3,2,1.
func integrationFloor() campaign.ExecutionBudgetRequirements {
	return campaign.ExecutionBudgetRequirements{
		MinUSD: 160_253_600, MinTokens: 161_268, MinModelCalls: 5,
		MinWallTimeMS: 1, MinDepth: 3, MinRetries: 1, MinSubagents: 5,
	}
}

// productionInfeasibleBudget is what Finance recommended in production
// (financial_review 3): representable, but unable to admit the first child.
func productionInfeasibleBudget() *campaign.BudgetRecommendation {
	return &campaign.BudgetRecommendation{MaxUSD: 0.05, MaxTokens: 10000, MaxModelCalls: 5, MaxWallTimeMS: 300000, MaxDepth: 2, MaxRetries: 1, MaxSubagents: 1}
}

func feasibleIntegrationBudget() *campaign.BudgetRecommendation {
	return &campaign.BudgetRecommendation{MaxUSD: 2.5, MaxTokens: 900000, MaxModelCalls: 40, MaxWallTimeMS: 3600000, MaxDepth: 5, MaxRetries: 3, MaxSubagents: 10}
}

func countRowsWhere(t *testing.T, ctx context.Context, f *chatFixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.store.Pool().QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// Finance recommends EXACTLY the production budget through the real Harness.
// Expected: one model invocation, FINANCE_BUDGET_INFEASIBLE, no review, no
// approval, a terminal non-retryable task, and no attempt loop.
func TestRealFinanceHarness_ProductionInfeasibleRecommendationFailsClosed(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	fx.requirements.set(integrationFloor())
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict: "recommended", Summary: "Production repro: valid dimensions, infeasible for the canonical execution.",
		RecommendedBudget: productionInfeasibleBudget(), Assumptions: []string{"none"}, Risks: []string{},
	})
	proposalID, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "feasibility-infeasible", goal)

	_, _, err := fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: taskID, ReviewRequestID: reviewRequestID,
	})
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("want ErrInfeasibleExecutionBudget, got: %v", err)
	}
	if errors.Is(err, campaign.ErrInvalidExecutionBudget) {
		t.Fatalf("the production budget is representable; it must not be reported invalid: %v", err)
	}

	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM model_invocations WHERE task_id=$1", taskID); got != 1 {
		t.Errorf("model invocations = %d, want exactly 1 (Finance is never blindly retried)", got)
	}
	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID); got != 0 {
		t.Errorf("CampaignFinancialReview rows = %d, want 0", got)
	}
	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2", chatTestOrganization, proposalID); got != 0 {
		t.Errorf("owner approvals = %d, want 0", got)
	}
	var status string
	if err := f.store.Pool().QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", taskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Errorf("task status = %q, want failed (terminal)", status)
	}
	var failureCode *string
	var retryable bool
	if err := f.store.Pool().QueryRow(ctx, "SELECT failure_code, retryable FROM task_attempts WHERE task_id=$1 ORDER BY id DESC LIMIT 1", taskID).Scan(&failureCode, &retryable); err != nil {
		t.Fatal(err)
	}
	if failureCode == nil || *failureCode != "FINANCE_BUDGET_INFEASIBLE" {
		t.Errorf("attempt failure_code = %v, want FINANCE_BUDGET_INFEASIBLE", failureCode)
	}
	if retryable {
		t.Error("the attempt must be non-retryable: the same floor would reject the same class of answer")
	}
	// No lease/attempt loop: exactly the one attempt that produced the answer.
	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM task_attempts WHERE task_id=$1", taskID); got != 1 {
		t.Errorf("task attempts = %d, want 1", got)
	}
}

// A budget at or above the floor goes through the same real stack and is
// persisted EXACTLY as recommended.
func TestRealFinanceHarness_FeasibleRecommendationIsRecordedUnchanged(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	fx.requirements.set(integrationFloor())
	service := f.withScriptedModel(t, &scriptedModel{})

	budget := feasibleIntegrationBudget()
	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict: "recommended", Summary: "Feasible: at or above every host minimum.",
		RecommendedBudget: budget, Assumptions: []string{"none"}, Risks: []string{},
		RequiredCorrections: []string{}, MissingInformation: []string{},
	})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "feasibility-feasible", goal)

	review, _, err := fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: taskID, ReviewRequestID: reviewRequestID,
	})
	if err != nil {
		t.Fatalf("a feasible recommendation must complete: %v", err)
	}
	if review.RecommendedBudget == nil || *review.RecommendedBudget != *budget {
		t.Fatalf("returned review budget %+v is not the recommended %+v", review.RecommendedBudget, budget)
	}
	durable, err := fx.store.GetFinancialReviewByRequestID(ctx, chatTestOrganization, reviewRequestID)
	if err != nil {
		t.Fatalf("read durable review: %v", err)
	}
	if durable.RecommendedBudget == nil || *durable.RecommendedBudget != *budget {
		t.Fatalf("durable review budget %+v is not the recommended %+v (no normalization allowed)", durable.RecommendedBudget, budget)
	}
	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM model_invocations WHERE task_id=$1", taskID); got != 1 {
		t.Errorf("model invocations = %d, want 1", got)
	}
	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID); got != 1 {
		t.Errorf("review rows = %d, want 1", got)
	}
	if got := countRowsWhere(t, ctx, f, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID); got != 1 {
		t.Errorf("completed task count = %d, want 1", got)
	}
}

// The host floor must actually REACH the provider: read the durable model
// input the dispatch sent (the canonical bytes Model Runtime persisted for the
// invocation) and require the floor in the provider-visible messages. A helper
// having received a struct proves nothing about what the model saw.
func TestRealFinanceHarness_ProviderVisibleInputStatesTheHostFloor(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	// Distinctive numbers so nothing can match by coincidence.
	floor := campaign.ExecutionBudgetRequirements{
		MinUSD: 424_242_000, MinTokens: 777_001, MinModelCalls: 7, MinWallTimeMS: 1, MinDepth: 4, MinRetries: 1, MinSubagents: 9,
	}
	fx.requirements.set(floor)
	service := f.withScriptedModel(t, &scriptedModel{})

	budget := &campaign.BudgetRecommendation{MaxUSD: 3, MaxTokens: 2_000_000, MaxModelCalls: 30, MaxWallTimeMS: 3600000, MaxDepth: 6, MaxRetries: 2, MaxSubagents: 20}
	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict: "recommended", Summary: "visibility", RecommendedBudget: budget, Assumptions: []string{"none"}, Risks: []string{},
	})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "feasibility-visible", goal)
	if _, _, err := fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: taskID, ReviewRequestID: reviewRequestID,
	}); err != nil {
		t.Fatalf("ExecuteReviewTask: %v", err)
	}

	var canonical []byte
	if err := f.store.Pool().QueryRow(ctx, `
		SELECT i.canonical_bytes FROM model_invocation_inputs i
		JOIN model_invocations m ON m.id = i.invocation_id WHERE m.task_id=$1`, taskID).Scan(&canonical); err != nil {
		t.Fatalf("read the durable provider-visible model input: %v", err)
	}
	var envelope modelruntime.ModelInputEnvelope
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		t.Fatalf("decode the model input envelope: %v", err)
	}
	var visible strings.Builder
	for _, message := range envelope.StablePrefix {
		visible.WriteString(message.Content)
		visible.WriteString("\n")
	}
	for _, want := range []string{
		"HOST EXECUTION BUDGET FLOOR (TRUSTED HOST FACTS)",
		"max_usd >= 0.424242",
		"max_tokens >= 777001",
		"max_model_calls >= 7",
		"max_wall_time_ms >= 1",
		"max_depth >= 4",
		"max_retries >= 1",
		"max_subagents >= 9",
		"you may NOT recommend less",
	} {
		if !strings.Contains(visible.String(), want) {
			t.Errorf("the provider-visible input does not contain %q -- Finance could claim it lacked the floor", want)
		}
	}
}

// The derivation itself, against the REAL canonical registry, the REAL routing
// tables and the REAL rate card (the migration-seeded prices production also
// uses) -- no injected requirement. Its facts must be the ones production
// measured: the CEO plan on openai_responses/gpt-5.6-luna, departments on
// gemini/gemini-3.8-flash, and a floor never below the $0.1602536
// reservation the first dispatch actually needed.
func TestExecutionRequirementsDeriveFromTheRealCanonicalFacts(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	provider := newDerivedExecutionRequirements(t, f.store, f.runtime.ModelRuntime)
	got, err := provider.ExecutionBudgetRequirements(ctx, chatTestOrganization)
	if err != nil {
		t.Fatalf("derive the floor from the real canonical facts: %v", err)
	}
	if got.MinModelCalls != 5 || got.MinSubagents != 5 || got.MinDepth != 3 || got.MinRetries != 1 || got.MinWallTimeMS != 1 {
		t.Fatalf("topology floor = calls %d, subagents %d, depth %d, retries %d, wall %d; want 5, 5, 3, 1, 1", got.MinModelCalls, got.MinSubagents, got.MinDepth, got.MinRetries, got.MinWallTimeMS)
	}
	if got.MinUSD < 160_253_600 || got.MinTokens < 33_268+128_000 {
		t.Fatalf("derived floor %s / %d tokens is below production's observed first reservation ($0.1602536 / 161,268 tokens)", got.MinUSD, got.MinTokens)
	}
	byStage := map[string]campaign.ExecutionStageBasis{}
	for _, stage := range got.Basis.Stages {
		byStage[stage.Stage] = stage
	}
	ceo := byStage["ceo_plan"]
	if ceo.ProviderID != "openai_responses" || ceo.ProviderModelID != "gpt-5.6-luna" || ceo.MaxOutputTokens != 128000 {
		t.Errorf("CEO-plan basis = %+v, want openai_responses/gpt-5.6-luna with Executive's 128000 output ceiling", ceo)
	}
	for _, name := range []string{"department_plan", "department_worker", "department_review"} {
		if stage := byStage[name]; stage.ProviderID != "gemini" {
			t.Errorf("%s basis = %+v, want a gemini route (department.leader / department.worker)", name, stage)
		}
	}
	if byStage["department_worker"].MaxOutputTokens != 24000 {
		t.Errorf("worker output ceiling = %d, want the worker-specific 24000", byStage["department_worker"].MaxOutputTokens)
	}
	t.Logf("REAL canonical floor: usd=%s tokens=%d (worst stage basis: %+v)", got.MinUSD, got.MinTokens, got.Basis.Stages)
}
