//go:build integration

package ceochat_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/campaign/executionrequirements"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

func permissiveRequirementsValue() campaign.ExecutionBudgetRequirements {
	return campaign.ExecutionBudgetRequirements{
		MinUSD: 1, MinTokens: 1, MinModelCalls: 1, MinWallTimeMS: 1, MinDepth: 1, MinRetries: 1, MinSubagents: 1,
	}
}

// permissiveExecutionRequirements is a floor every fixture budget clears, for
// integration tests that are not about execution-budget feasibility.
func permissiveExecutionRequirements() campaign.ExecutionRequirementsProvider {
	return campaign.FixedExecutionRequirements{Requirements: permissiveRequirementsValue()}
}

// mutableRequirements is an injected host floor a test can move between
// Finance, approval and promotion -- how a routing/pricing/runtime-limit drift
// is staged without touching the real registry. It starts permissive.
type mutableRequirements struct {
	mu    sync.Mutex
	value campaign.ExecutionBudgetRequirements
}

func newMutableRequirements() *mutableRequirements {
	return &mutableRequirements{value: permissiveRequirementsValue()}
}

func (m *mutableRequirements) set(value campaign.ExecutionBudgetRequirements) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.value = value
}

func (m *mutableRequirements) ExecutionBudgetRequirements(context.Context, string) (campaign.ExecutionBudgetRequirements, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value, nil
}

// newDerivedExecutionRequirements is the REAL derivation (Executive topology,
// canonical routing, CostGate's own estimator, Executive limits, Context Engine
// bound) over the fixture's real database -- the composition production uses.
func newDerivedExecutionRequirements(t *testing.T, store *platformpostgres.Store, modelRuntime *modelbootstrap.Runtime) campaign.ExecutionRequirementsProvider {
	t.Helper()
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatalf("open registry repository: %v", err)
	}
	cfg := buildTestFinanceE2EConfig(t, chatTestOrganization)
	provider, err := executionrequirements.New(executionrequirements.Config{
		Registry: registryRepo, Routes: modelRuntime.Store, Costs: modelRuntime.Costs,
		Limits: executive.DefaultLimits(), ContextMaxTotalBytes: cfg.Context.MaxTotalBytes,
	})
	if err != nil {
		t.Fatalf("create execution requirements provider: %v", err)
	}
	return provider
}

// budgetAboveFloor recommends twice every derived minimum: strictly feasible,
// still a small, fully representable budget.
func budgetAboveFloor(floor campaign.ExecutionBudgetRequirements) campaign.BudgetRecommendation {
	// Round UP to whole micro-dollars so the doubled figure is never truncated
	// below twice the floor by float conversion.
	micros := (2*int64(floor.MinUSD) + 999) / 1000
	// Above both floors: a budget "above the floor" that a governed promotion then refuses proves
	// nothing about the mode. The governed token floor (one worst reservation per governed call)
	// is far above the analysis one.
	return campaign.BudgetRecommendation{
		MaxUSD:        float64(micros) / 1_000_000,
		MaxTokens:     2 * max(floor.MinTokens, floor.GovernedMinTokens),
		MaxModelCalls: int(2 * max(floor.MinModelCalls, floor.GovernedMinModelCalls)),
		MaxWallTimeMS: 7_200_000,
		MaxDepth:      int(floor.MinDepth) + 2,
		MaxRetries:    int(floor.MinRetries) + 3,
		MaxSubagents:  int(max(floor.MinSubagents, floor.GovernedMinSubagents)) + 3,
	}
}

// budgetBelowFloor is a representable budget (every dimension positive) that the derived floor
// refuses: half its USD and token minimums, and one model call and one subagent short. Derived, so
// it stays below the floor whichever way routing, pricing or topology move it; a fixed "small"
// budget is infeasible only while the floor happens to sit above it.
func budgetBelowFloor(floor campaign.ExecutionBudgetRequirements) campaign.BudgetRecommendation {
	budget := budgetAboveFloor(floor)
	// Whole micro-dollars below half the floor, never zero.
	micros := max(int64(floor.MinUSD)/2/1000, 1)
	budget.MaxUSD = float64(micros) / 1_000_000
	budget.MaxTokens = max(floor.MinTokens/2, 1)
	budget.MaxModelCalls = int(max(floor.MinModelCalls-1, 1))
	budget.MaxSubagents = int(max(floor.MinSubagents-1, 1))
	return budget
}
