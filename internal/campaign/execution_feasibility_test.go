package campaign

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// productionInfeasibleBudget is EXACTLY what Finance recommended in the
// production smoke (financial_review 3 / owner_approval 3): every dimension
// strictly positive -- so it is a perfectly valid AgentBudget -- yet unable to
// admit the first Executive child it was approved to fund.
func productionInfeasibleBudget() BudgetRecommendation {
	return BudgetRecommendation{
		MaxUSD: 0.05, MaxTokens: 10000, MaxModelCalls: 5, MaxWallTimeMS: 300000,
		MaxDepth: 2, MaxRetries: 1, MaxSubagents: 1,
	}
}

// canonicalFloorFixture mirrors the canonical minimum execution's constraints
// as production measured them: the first CEO-plan dispatch reserved
// $0.1602536 (33,268 estimated input tokens + the 128,000-token output
// ceiling), and the minimal tree is five children attached at depths 1,2,3,2,1.
// These numbers are TEST EVIDENCE, not policy: the runtime derives its floor
// from the host (see internal/campaign/executionrequirements).
func canonicalFloorFixture() ExecutionBudgetRequirements {
	return ExecutionBudgetRequirements{
		MinUSD: 160_253_600, MinTokens: 33_268 + 128_000, MinModelCalls: 5,
		MinWallTimeMS: 1, MinDepth: 3, MinRetries: 1, MinSubagents: 5,
	}
}

// budgetAtFloor builds the smallest recommendation that satisfies req, through
// the same dollar rendering the Finance prompt uses.
func budgetAtFloor(t *testing.T, req ExecutionBudgetRequirements) BudgetRecommendation {
	t.Helper()
	usd, err := parseDollars(minimumUSDDollars(req.MinUSD))
	if err != nil {
		t.Fatal(err)
	}
	return BudgetRecommendation{
		MaxUSD: usd, MaxTokens: req.MinTokens, MaxModelCalls: int(req.MinModelCalls), MaxWallTimeMS: req.MinWallTimeMS,
		MaxDepth: int(req.MinDepth), MaxRetries: int(req.MinRetries), MaxSubagents: int(req.MinSubagents),
	}
}

// The mandatory distinction: the production budget is REPRESENTABLE and
// INFEASIBLE. Passing the first must never imply the second.
func TestProductionRecommendationIsRepresentableButInfeasible(t *testing.T) {
	budget := productionInfeasibleBudget()
	if err := ValidateExecutableBudget(budget); err != nil {
		t.Fatalf("ValidateExecutableBudget must PASS the production budget (all seven dimensions positive): %v", err)
	}
	err := ValidateExecutionBudgetFeasibility(budget, canonicalFloorFixture())
	if err == nil {
		t.Fatal("ValidateExecutionBudgetFeasibility must FAIL the production budget")
	}
	if !errors.Is(err, ErrInfeasibleExecutionBudget) {
		t.Fatalf("errors.Is(err, ErrInfeasibleExecutionBudget) = false: %v", err)
	}
	if errors.Is(err, ErrInvalidExecutionBudget) {
		t.Fatalf("an infeasible budget must NOT be reported as an invalid one: %v", err)
	}
	// Exactly the four dimensions production proved short, and no others.
	for _, want := range []string{"max_usd", "max_tokens", "max_depth", "max_subagents"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name the short dimension %s: %v", want, err)
		}
	}
	for _, notShort := range []string{"max_model_calls", "max_wall_time_ms", "max_retries"} {
		if strings.Contains(err.Error(), notShort) {
			t.Errorf("error names %s, which production's budget satisfied: %v", notShort, err)
		}
	}
}

func TestInvalidAndInfeasibleAreDistinctSentinels(t *testing.T) {
	zeroSubagents := productionInfeasibleBudget()
	zeroSubagents.MaxSubagents = 0
	err := ValidateExecutionBudgetFeasibility(zeroSubagents, canonicalFloorFixture())
	if !errors.Is(err, ErrInvalidExecutionBudget) || errors.Is(err, ErrInfeasibleExecutionBudget) {
		t.Fatalf("max_subagents=0 must be ErrInvalidExecutionBudget only, got: %v", err)
	}
	if errors.Is(ErrInfeasibleExecutionBudget, ErrInvalidExecutionBudget) || errors.Is(ErrInvalidExecutionBudget, ErrInfeasibleExecutionBudget) {
		t.Fatal("the two sentinels must not wrap one another")
	}
}

// Starting from a budget at the exact floor (which passes), each dimension
// independently set one unit below its minimum must fail as infeasible.
func TestFeasibilityIsCheckedDimensionByDimension(t *testing.T) {
	req := canonicalFloorFixture()
	req.MinWallTimeMS = 2 // a floor of 1 cannot go one below and stay representable; 2 can.
	at := budgetAtFloor(t, req)
	if err := ValidateExecutionBudgetFeasibility(at, req); err != nil {
		t.Fatalf("a budget at the exact floor must pass: %v", err)
	}
	mutations := map[string]func(*BudgetRecommendation){
		"max_usd":          func(b *BudgetRecommendation) { b.MaxUSD -= 0.000001 },
		"max_tokens":       func(b *BudgetRecommendation) { b.MaxTokens-- },
		"max_model_calls":  func(b *BudgetRecommendation) { b.MaxModelCalls-- },
		"max_wall_time_ms": func(b *BudgetRecommendation) { b.MaxWallTimeMS-- },
		"max_depth":        func(b *BudgetRecommendation) { b.MaxDepth-- },
		"max_subagents":    func(b *BudgetRecommendation) { b.MaxSubagents-- },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			below := at
			mutate(&below)
			if err := ValidateExecutableBudget(below); err != nil {
				t.Fatalf("test setup: the mutated budget must stay representable: %v", err)
			}
			err := ValidateExecutionBudgetFeasibility(below, req)
			if !errors.Is(err, ErrInfeasibleExecutionBudget) {
				t.Fatalf("one unit below the %s floor must be infeasible, got: %v", name, err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("error should name %s: %v", name, err)
			}
		})
	}

	// Retries: the floor is the strict-positive minimum (nothing in the
	// canonical Executive path consumes retries), so "below" is zero, which
	// is REPRESENTABILITY's failure, not feasibility's -- the two contracts
	// agree there and neither is weakened.
	zeroRetries := at
	zeroRetries.MaxRetries = 0
	if err := ValidateExecutionBudgetFeasibility(zeroRetries, req); !errors.Is(err, ErrInvalidExecutionBudget) {
		t.Fatalf("max_retries=0 must fail representability, got: %v", err)
	}
	// A stronger retries floor, if the host ever derives one, is enforced too.
	strongerRetries := req
	strongerRetries.MinRetries = 3
	if err := ValidateExecutionBudgetFeasibility(at, strongerRetries); !errors.Is(err, ErrInfeasibleExecutionBudget) || !strings.Contains(err.Error(), "max_retries") {
		t.Fatalf("a retries floor above the budget must be infeasible on max_retries, got: %v", err)
	}
}

// Larger is always fine: the model may recommend MORE than the floor.
func TestFeasibilityAcceptsAnyBudgetAboveTheFloor(t *testing.T) {
	req := canonicalFloorFixture()
	generous := budgetAtFloor(t, req)
	generous.MaxUSD *= 10
	generous.MaxTokens *= 10
	generous.MaxModelCalls *= 3
	generous.MaxDepth += 4
	generous.MaxSubagents += 10
	generous.MaxRetries = 9
	generous.MaxWallTimeMS *= 100
	if err := ValidateExecutionBudgetFeasibility(generous, req); err != nil {
		t.Fatalf("a budget above every minimum must be feasible: %v", err)
	}
}

// The USD floor is judged on the EXACT nano value the budget converts to, and
// the dollar figure shown to Finance must round-trip to at least the floor.
// USDFromDollars truncates a float64 product, so printing min/1e9 naively can
// land one nano short and reject a model that copied the floor to the letter.
func TestMinimumUSDDollarsNeverRoundTripsBelowTheFloor(t *testing.T) {
	samples := []modelpricing.USDNanos{1, 999, 1_000, 50_000_000, 160_253_600, 160_253_601, 370_249_999, 1_000_000_000, 4_500_000_000_000}
	rng := rand.New(rand.NewSource(20260919))
	for i := 0; i < 20000; i++ {
		samples = append(samples, modelpricing.USDNanos(1+rng.Int63n(50_000_000_000)))
	}
	for _, min := range samples {
		text := minimumUSDDollars(min)
		dollars, err := parseDollars(text)
		if err != nil {
			t.Fatalf("minimumUSDDollars(%d) = %q is not a number: %v", min, text, err)
		}
		if got := modelpricing.USDFromDollars(dollars); got < min {
			t.Fatalf("minimumUSDDollars(%d) = %q converts to %d nanos, below the floor", min, text, got)
		}
		// ...and it is not padded: at most a few micro-dollars above the floor.
		if over := int64(modelpricing.USDFromDollars(dollars)) - int64(min); over > 2_000 {
			t.Fatalf("minimumUSDDollars(%d) = %q overshoots the floor by %d nanos", min, text, over)
		}
	}
	if got := minimumUSDDollars(160_253_600); got != "0.160254" {
		t.Fatalf("minimumUSDDollars(160253600) = %q, want 0.160254", got)
	}
}

func TestRequirementsMustBeStrictlyPositiveAndAvailable(t *testing.T) {
	for _, name := range []string{"usd", "tokens", "calls", "wall", "depth", "retries", "subagents"} {
		req := canonicalFloorFixture()
		switch name {
		case "usd":
			req.MinUSD = 0
		case "tokens":
			req.MinTokens = 0
		case "calls":
			req.MinModelCalls = 0
		case "wall":
			req.MinWallTimeMS = 0
		case "depth":
			req.MinDepth = 0
		case "retries":
			req.MinRetries = 0
		case "subagents":
			req.MinSubagents = 0
		}
		if err := ValidateExecutionBudgetFeasibility(budgetAtFloor(t, canonicalFloorFixture()), req); !errors.Is(err, ErrExecutionRequirementsUnavailable) {
			t.Fatalf("a zero %s floor would silently disable that check and must be refused, got: %v", name, err)
		}
	}
	ctx := context.Background()
	if _, err := requireExecutionRequirements(ctx, nil, "org"); !errors.Is(err, ErrExecutionRequirementsUnavailable) {
		t.Fatalf("no provider must fail closed, got: %v", err)
	}
	failing := failingRequirements{err: errors.New("pricing unavailable")}
	if _, err := requireExecutionRequirements(ctx, failing, "org"); !errors.Is(err, ErrExecutionRequirementsUnavailable) {
		t.Fatalf("a provider error must fail closed, got: %v", err)
	}
	if got, err := requireExecutionRequirements(ctx, FixedExecutionRequirements{Requirements: canonicalFloorFixture()}, "org"); err != nil || got.MinSubagents != 5 {
		t.Fatalf("a valid provider must resolve: %+v, %v", got, err)
	}
}

type failingRequirements struct{ err error }

func (f failingRequirements) ExecutionBudgetRequirements(context.Context, string) (ExecutionBudgetRequirements, error) {
	return ExecutionBudgetRequirements{}, f.err
}

// validateFinanceReviewOutput is the host gate Finance's output passes BEFORE
// success, review persistence or task completion.
func TestFinanceOutputGateAppliesTheFloorOnlyToRecommended(t *testing.T) {
	req := canonicalFloorFixture()
	under := productionInfeasibleBudget()

	recommendedUnder := FinanceReviewOutput{Verdict: string(VerdictRecommended), Summary: "s", RecommendedBudget: &under}
	if err := validateFinanceReviewOutput(recommendedUnder, req); !errors.Is(err, ErrInfeasibleExecutionBudget) {
		t.Fatalf("recommended + under-floor budget must be infeasible, got: %v", err)
	}
	atFloor := budgetAtFloor(t, req)
	if err := validateFinanceReviewOutput(FinanceReviewOutput{Verdict: string(VerdictRecommended), RecommendedBudget: &atFloor}, req); err != nil {
		t.Fatalf("recommended + at-floor budget must pass: %v", err)
	}
	if err := validateFinanceReviewOutput(FinanceReviewOutput{Verdict: string(VerdictRecommended)}, req); !errors.Is(err, ErrInvalidExecutionBudget) {
		t.Fatalf("recommended without a budget stays ErrInvalidExecutionBudget, got: %v", err)
	}
	// Non-recommended verdicts keep exactly the existing rules: no budget is
	// fine, a representable-but-small budget is fine (nothing launches on it),
	// a non-representable budget is still refused.
	for _, verdict := range []FinancialReviewVerdict{VerdictChangesRequested, VerdictNotRecommended, VerdictInsufficientData} {
		if err := validateFinanceReviewOutput(FinanceReviewOutput{Verdict: string(verdict), Summary: "s"}, req); err != nil {
			t.Fatalf("%s with no budget must stay valid: %v", verdict, err)
		}
		if err := validateFinanceReviewOutput(FinanceReviewOutput{Verdict: string(verdict), RecommendedBudget: &under}, req); err != nil {
			t.Fatalf("%s with a representable but small budget must stay valid (no floor for a budget that is not recommended): %v", verdict, err)
		}
		zero := under
		zero.MaxSubagents = 0
		if err := validateFinanceReviewOutput(FinanceReviewOutput{Verdict: string(verdict), RecommendedBudget: &zero}, req); !errors.Is(err, ErrInvalidExecutionBudget) {
			t.Fatalf("%s with a zero-dimension budget must stay ErrInvalidExecutionBudget, got: %v", verdict, err)
		}
	}
}

// The host floor is a TRUSTED HOST FACT inside the execution-contract text the
// Finance model receives, in the exact units of recommended_budget.
func TestFinanceContractStatesTheHostFloor(t *testing.T) {
	req := canonicalFloorFixture()
	text := renderFinanceContractInstructions(req)
	for _, want := range []string{
		"HOST EXECUTION BUDGET FLOOR (TRUSTED HOST FACTS)",
		"max_usd >= 0.160254",
		"max_tokens >= 161268",
		"max_model_calls >= 5",
		"max_wall_time_ms >= 1",
		"max_depth >= 3",
		"max_retries >= 1",
		"max_subagents >= 5",
		"You MAY recommend more than a minimum; you may NOT recommend less",
		"not a prediction of what the campaign will actually spend",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("contract instructions are missing %q", want)
		}
	}
	// Changing a host minimum changes what Finance is shown.
	req.MinSubagents = 9
	if !strings.Contains(renderFinanceContractInstructions(req), "max_subagents >= 9") {
		t.Error("the rendered floor must follow the requirements it was given")
	}
	// The JSON shape example must not teach a below-floor budget.
	if strings.Contains(text, `"max_depth": 1,`) || strings.Contains(text, `"max_subagents": 1
`) {
		t.Error("the schema example still shows the old below-floor sample values")
	}
}
