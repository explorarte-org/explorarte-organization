package executionrequirements_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/campaign/executionrequirements"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/costgate"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

const org = "explorarte"

// --- fakes -----------------------------------------------------------------

type fakeRegistry struct {
	registry.Reader
	revision *registry.Revision
	units    []registry.Unit
	roles    map[string]registry.Role
	err      error
}

func (f *fakeRegistry) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	return f.revision, f.err
}
func (f *fakeRegistry) ListUnits(context.Context, string) ([]registry.Unit, error) {
	return f.units, nil
}
func (f *fakeRegistry) GetRole(_ context.Context, _ string, id string) (registry.Role, error) {
	role, ok := f.roles[id]
	if !ok {
		return registry.Role{}, registry.ErrNotFound
	}
	return role, nil
}
func (f *fakeRegistry) GetLeader(_ context.Context, _ string, unit string) (registry.Role, error) {
	for _, u := range f.units {
		if u.ID == unit && u.LeaderRoleID != nil {
			return f.GetRole(context.Background(), org, *u.LeaderRoleID)
		}
	}
	return registry.Role{}, registry.ErrNotFound
}
func (f *fakeRegistry) ListRoles(_ context.Context, _ string, filter registry.RoleFilter) ([]registry.Role, error) {
	var out []registry.Role
	for _, role := range f.roles {
		if filter.UnitID != "" && role.UnitID != filter.UnitID {
			continue
		}
		if filter.EnabledOnly && !role.Enabled {
			continue
		}
		out = append(out, role)
	}
	return out, nil
}

// fakeRoutes is Model Runtime's canonical routing state: static bindings per
// role and pool candidates per policy.
type fakeRoutes struct {
	modelruntime.RegistryStore
	static map[string]modelruntime.RoutableModel // role -> binding
	pools  map[string][]modelruntime.RoutableModel
}

func (f *fakeRoutes) GetRoutingPolicy(_ context.Context, _ string, _ int64, policy string) (modelruntime.RoutingPolicy, bool, error) {
	if _, ok := f.pools[policy]; ok {
		return modelruntime.RoutingPolicy{PolicyID: policy, RoutingMode: modelruntime.RoutingModePool}, true, nil
	}
	return modelruntime.RoutingPolicy{}, false, nil
}
func (f *fakeRoutes) GetBinding(_ context.Context, _ string, _ int64, role string) (modelruntime.ResolvedBinding, error) {
	m, ok := f.static[role]
	if !ok {
		return modelruntime.ResolvedBinding{}, modelruntime.ErrBindingNotFound
	}
	return modelruntime.ResolvedBinding{Version: modelruntime.ProfileVersion{ProviderID: m.ProviderID, ProviderModelID: m.ProviderModelID}}, nil
}
func (f *fakeRoutes) ListRoutingCandidates(_ context.Context, _ string, _ int64, policy string) ([]modelruntime.RoutingCandidate, error) {
	var out []modelruntime.RoutingCandidate
	for _, m := range f.pools[policy] {
		out = append(out, modelruntime.RoutingCandidate{PolicyID: policy, ProviderID: m.ProviderID, ProviderModelID: m.ProviderModelID})
	}
	return out, nil
}

type memoryPricing struct{ tiers []modelpricing.PriceTier }

func (m *memoryPricing) ListTiers(_ context.Context, p, model string, mode modelpricing.BillingMode, _ time.Time) ([]modelpricing.PriceTier, error) {
	var out []modelpricing.PriceTier
	for _, tier := range m.tiers {
		if tier.ProviderID == p && tier.ProviderModelID == model && tier.BillingMode == mode {
			out = append(out, tier)
		}
	}
	return out, nil
}
func (m *memoryPricing) Upsert(_ context.Context, tier modelpricing.PriceTier) (modelpricing.PriceTier, error) {
	return tier, nil
}

type amountLedger struct {
	costledger.Ledger
	usd []modelpricing.USDNanos
}

func (l *amountLedger) Reserve(_ context.Context, _ string, _ int64, usd modelpricing.USDNanos, _ time.Time) error {
	l.usd = append(l.usd, usd)
	return nil
}

type amountBudgets struct {
	agentbudget.Ledger
	consumed []agentbudget.Usage
}

func (b *amountBudgets) ResolveBudgetForTask(context.Context, int64) (agentbudget.Budget, error) {
	return agentbudget.Budget{ID: 1}, nil
}
func (b *amountBudgets) ConsumeModelCall(_ context.Context, _, _ int64, d agentbudget.Usage, _ time.Time) error {
	b.consumed = append(b.consumed, d)
	return nil
}

// --- the production-shaped world ---------------------------------------------

func tier(p, m, name string, min int64, in, out int64) modelpricing.PriceTier {
	return modelpricing.PriceTier{ProviderID: p, ProviderModelID: m, ContextTierName: name, MinInputTokens: min,
		InputPriceNanosPerMillion: modelpricing.USDNanos(in), OutputPriceNanosPerMillion: modelpricing.USDNanos(out),
		BillingMode: modelpricing.BillingOnline, EffectiveAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}
}

func productionPricing() *memoryPricing {
	return &memoryPricing{tiers: []modelpricing.PriceTier{
		// openai_responses/gpt-5.6-luna, production's CEO route
		tier("openai_responses", "gpt-5.6-luna", "default", 0, 200_000_000, 1_200_000_000),
		tier("openai_responses", "gpt-5.6-luna", "long_context", 272_000, 400_000_000, 1_800_000_000),
		// gemini/gemini-3.5-flash-lite, production's leader and worker route
		tier("gemini", "gemini-3.5-flash-lite", "default", 0, 300_000_000, 2_500_000_000),
		// a pricey route no eligible role reaches
		tier("xai", "grok-4.6", "default", 0, 2_000_000_000, 6_000_000_000),
	}}
}

func strPtr(s string) *string { return &s }

func productionWorld() (*fakeRegistry, *fakeRoutes) {
	role := func(id, unit, policy string, leader bool, class string) registry.Role {
		return registry.Role{ID: id, UnitID: unit, ModelPolicy: strPtr(policy), CanonicalLeader: leader, Enabled: true, Executable: true, AuthorityClass: class}
	}
	reg := &fakeRegistry{
		revision: &registry.Revision{ID: 5},
		units: []registry.Unit{
			{ID: "ingenieria_ia", Operational: true, LeaderRoleID: strPtr("ingenieria_ia/orquestador")},
			{ID: "negocio", Operational: true, LeaderRoleID: strPtr("negocio/orquestador")},
			// not delegable: never contributes to the floor
			{ID: "investigacion", Operational: false, LeaderRoleID: strPtr("investigacion/lead")},
			{ID: "retirada", Operational: true, LeaderRoleID: strPtr("retirada/lead"), RetiredAt: func() *time.Time { n := time.Now(); return &n }()},
		},
		roles: map[string]registry.Role{
			executive.CEORoleID:         role(executive.CEORoleID, "empresa", "executive.ceo", true, ""),
			"ingenieria_ia/orquestador": role("ingenieria_ia/orquestador", "ingenieria_ia", "department.leader", true, ""),
			"ingenieria_ia/backend":     role("ingenieria_ia/backend", "ingenieria_ia", "department.worker", false, ""),
			"ingenieria_ia/code-runner": role("ingenieria_ia/code-runner", "ingenieria_ia", "research.adversarial_review", false, "execution_service"),
			"negocio/orquestador":       role("negocio/orquestador", "negocio", "department.leader", true, ""),
			"negocio/analista":          role("negocio/analista", "negocio", "department.worker", false, ""),
			"investigacion/lead":        role("investigacion/lead", "investigacion", "research.adversarial_review", true, ""),
			"retirada/lead":             role("retirada/lead", "retirada", "research.adversarial_review", true, ""),
			"negocio/sin-politica":      {ID: "negocio/sin-politica", UnitID: "negocio", Enabled: true, Executable: true},
			"negocio/sin-binding":       role("negocio/sin-binding", "negocio", "department.worker", false, ""),
		},
	}
	luna := modelruntime.RoutableModel{ProviderID: "openai_responses", ProviderModelID: "gpt-5.6-luna"}
	flash := modelruntime.RoutableModel{ProviderID: "gemini", ProviderModelID: "gemini-3.5-flash-lite"}
	grok := modelruntime.RoutableModel{ProviderID: "xai", ProviderModelID: "grok-4.6"}
	routes := &fakeRoutes{static: map[string]modelruntime.RoutableModel{
		executive.CEORoleID:         luna,
		"ingenieria_ia/orquestador": flash, "ingenieria_ia/backend": flash, "ingenieria_ia/code-runner": grok,
		"negocio/orquestador": flash, "negocio/analista": flash,
		"investigacion/lead": grok, "retirada/lead": grok,
		// negocio/sin-binding has NO binding: it can never reach a provider.
	}}
	return reg, routes
}

func newProvider(t *testing.T, reg *fakeRegistry, routes *fakeRoutes, pricing *memoryPricing, mutate func(*executionrequirements.Config)) (*executionrequirements.Provider, *costgate.Gate) {
	t.Helper()
	service, err := modelpricing.NewService(pricing)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := costgate.New(service, &amountLedger{}, &amountBudgets{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := executionrequirements.Config{
		Registry: reg, Routes: routes, Costs: gate, Limits: executive.DefaultLimits(), ContextMaxTotalBytes: 524288,
		Clock: func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	provider, err := executionrequirements.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return provider, gate
}

// --- tests -----------------------------------------------------------------

// The topology part of the floor is Executive's declaration, not Campaign's.
func TestTopologyFloorComesFromExecutive(t *testing.T) {
	reg, routes := productionWorld()
	provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
	got, err := provider.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	topology := executive.MinimalCampaignTopology()
	if got.MinModelCalls != topology.ModelCalls || got.MinSubagents != topology.Subagents || got.MinDepth != topology.Depth {
		t.Fatalf("floor calls/subagents/depth = %d/%d/%d, Executive declares %d/%d/%d",
			got.MinModelCalls, got.MinSubagents, got.MinDepth, topology.ModelCalls, topology.Subagents, topology.Depth)
	}
	if got.MinModelCalls != 5 || got.MinSubagents != 5 || got.MinDepth != 3 {
		t.Fatalf("today's minimal campaign is 5 calls, 5 subagents, depth 3; got %d/%d/%d", got.MinModelCalls, got.MinSubagents, got.MinDepth)
	}
	// Wall time and retries are not consumed on this path: no invented floor.
	if got.MinWallTimeMS != 1 || got.MinRetries != 1 || got.Basis.WallTimeConsumed || got.Basis.RetriesConsumed {
		t.Fatalf("wall/retries floor must be the validity minimum of 1 and marked unconsumed: %+v", got)
	}
	if got.Basis.OrganizationRevisionID != 5 {
		t.Fatalf("basis revision = %d, want 5", got.Basis.OrganizationRevisionID)
	}
}

// The dynamic part is derived from the same canonical facts a real dispatch
// uses -- checked here against an arithmetic derivation that shares no code
// with the provider: the worst stage is a department call on gemini flash-lite
// (2.5 $/M output x 128,000), not the CEO plan on gpt-5.6-luna, and the
// long-context tier applies because the input ceiling crosses 272,000 tokens.
func TestDynamicFloorIsTheWorstCanonicalStageNotJustTheFirst(t *testing.T) {
	reg, routes := productionWorld()
	provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
	got, err := provider.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	inputBytes := func(p executive.ExecutionPurpose) int {
		n, err := modelruntime.SingleShotModelInputCeilingBytes(524288, executive.ExecutionContractBytes(p))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	tokens := func(p executive.ExecutionPurpose) int64 { return int64(inputBytes(p))*2/3 + 1 } // the dispatch rule, restated
	usd := func(in, out, inRate, outRate int64) modelpricing.USDNanos {
		return modelpricing.USDNanos(in*inRate/1_000_000 + out*outRate/1_000_000)
	}

	ceoIn := tokens(executive.PurposeCEOPlan)
	if ceoIn < 272_000 {
		t.Fatalf("test premise: the CEO input ceiling %d should cross the long-context threshold", ceoIn)
	}
	ceoUSD := usd(ceoIn, 128_000, 400_000_000, 1_800_000_000) // long_context tier
	// Both leader stages run on flash-lite with a 128,000 output ceiling; the
	// review carries the longer host contract, so it is the worst of the two.
	planIn, planUSD := tokens(executive.PurposeDepartmentPlan), usd(tokens(executive.PurposeDepartmentPlan), 128_000, 300_000_000, 2_500_000_000)
	if reviewIn := tokens(executive.PurposeDepartmentReview); reviewIn > planIn {
		planIn, planUSD = reviewIn, usd(reviewIn, 128_000, 300_000_000, 2_500_000_000)
	}
	workerIn := tokens(executive.PurposeDepartmentWorker)
	workerUSD := usd(workerIn, 24_000, 300_000_000, 2_500_000_000)
	if planUSD <= ceoUSD {
		t.Fatalf("test premise: a leader stage (%s) should out-price the CEO plan (%s)", planUSD, ceoUSD)
	}
	if got.MinUSD != planUSD {
		t.Fatalf("MinUSD = %s, want the worst stage's worst-case reservation %s (ceo %s, worker %s)", got.MinUSD, planUSD, ceoUSD, workerUSD)
	}
	wantTokens := planIn + 128_000
	if ceoTokens := ceoIn + 128_000; ceoTokens > wantTokens {
		wantTokens = ceoTokens
	}
	if got.MinTokens != wantTokens {
		t.Fatalf("MinTokens = %d, want %d", got.MinTokens, wantTokens)
	}

	// Provenance names the stages and routes the numbers came from, and
	// ineligible roles (execution service, non-delegable/retired units) with
	// far pricier routes never contributed.
	if len(got.Basis.Stages) != 5 {
		t.Fatalf("basis has %d stages, want 5", len(got.Basis.Stages))
	}
	for _, stage := range got.Basis.Stages {
		if stage.ProviderID == "xai" {
			t.Fatalf("an ineligible role's route leaked into the floor: %+v", stage)
		}
		if stage.Purpose == string(executive.PurposeCEOPlan) && (stage.PriceTier != "long_context" || stage.ProviderModelID != "gpt-5.6-luna") {
			t.Fatalf("CEO stage basis = %+v", stage)
		}
	}
}

// The floor is never below what a real dispatch reserved in production: the
// observed first CEO-plan reservation was $0.1602536 for 33,268 input tokens
// and a 128,000 output ceiling.
func TestFloorIsNeverBelowTheProductionObservedReservation(t *testing.T) {
	reg, routes := productionWorld()
	provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
	got, err := provider.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if got.MinUSD < 160_253_600 || got.MinTokens < 33_268+128_000 {
		t.Fatalf("floor %s / %d tokens is below production's observed first reservation", got.MinUSD, got.MinTokens)
	}
}

// PREFLIGHT PARITY: for every stage the floor reports, the real CostGate's
// Reserve -- fed the same route, token estimate and output ceiling -- charges
// EXACTLY the estimate the floor was built from, in nanos, and the same token
// delta. There is no second pricing formula to drift.
func TestFloorMatchesWhatTheRealCostGateReserves(t *testing.T) {
	reg, routes := productionWorld()
	pricing := productionPricing()
	service, _ := modelpricing.NewService(pricing)
	ledger, budgets := &amountLedger{}, &amountBudgets{}
	gate, err := costgate.New(service, ledger, budgets)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := executionrequirements.New(executionrequirements.Config{
		Registry: reg, Routes: routes, Costs: gate, Limits: executive.DefaultLimits(), ContextMaxTotalBytes: 524288,
		Clock: func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := provider.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	for i, stage := range got.Basis.Stages {
		if _, err := gate.Reserve(context.Background(), modelruntime.CostReservationRequest{
			OrganizationID: org, TaskID: int64(100 + i), InvocationID: int64(200 + i),
			ProviderID: stage.ProviderID, ProviderModelID: stage.ProviderModelID,
			EstimatedInputTokens: stage.EstimatedInputTokens, MaxOutputTokens: stage.MaxOutputTokens,
		}, time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("stage %s: %v", stage.Stage, err)
		}
		if ledger.usd[i] != stage.ReservationUSD {
			t.Errorf("stage %s: CostGate reserved %s, floor used %s", stage.Stage, ledger.usd[i], stage.ReservationUSD)
		}
		if budgets.consumed[i].UsedTokens != stage.ReservationTokens || budgets.consumed[i].UsedUSD != stage.ReservationUSD {
			t.Errorf("stage %s: AgentBudget charge %+v, floor used %s / %d tokens", stage.Stage, budgets.consumed[i], stage.ReservationUSD, stage.ReservationTokens)
		}
		// And the input estimate is Model Runtime's own dispatch rule.
		if stage.EstimatedInputTokens != modelruntime.EstimateInputTokens(stage.InputBytes) {
			t.Errorf("stage %s: input estimate is not the dispatch rule", stage.Stage)
		}
	}
}

// A change to Executive's canonical limit or to the Context Engine bound moves
// the floor automatically; nothing here is a Finance-owned constant.
func TestFloorFollowsExecutiveLimitsAndTheContextBound(t *testing.T) {
	reg, routes := productionWorld()
	base, _ := newProvider(t, reg, routes, productionPricing(), nil)
	baseline, err := base.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}

	smallOut, _ := newProvider(t, reg, routes, productionPricing(), func(c *executionrequirements.Config) {
		c.Limits.MaxOutputTokens = 32_000
	})
	lower, err := smallOut.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if !(lower.MinUSD < baseline.MinUSD && lower.MinTokens < baseline.MinTokens) {
		t.Fatalf("lowering Executive's output ceiling must lower the floor: %s/%d vs %s/%d", lower.MinUSD, lower.MinTokens, baseline.MinUSD, baseline.MinTokens)
	}
	// The CEO-plan basis carries exactly Limits.MaxOutputTokensFor(PurposeCEOPlan).
	if lower.Basis.Stages[0].MaxOutputTokens != 32_000 {
		t.Fatalf("CEO-plan output ceiling = %d, want Executive's 32000", lower.Basis.Stages[0].MaxOutputTokens)
	}

	smallCtx, _ := newProvider(t, reg, routes, productionPricing(), func(c *executionrequirements.Config) { c.ContextMaxTotalBytes = 65536 })
	tight, err := smallCtx.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if !(tight.MinTokens < baseline.MinTokens && tight.MinUSD < baseline.MinUSD) {
		t.Fatalf("a smaller context bound must lower the floor: %s/%d vs %s/%d", tight.MinUSD, tight.MinTokens, baseline.MinUSD, baseline.MinTokens)
	}
}

// PRICING / ROUTING DRIFT: the same registry, a different price or route, a
// different floor -- so a floor recorded earlier can go stale, which is
// exactly what Approval and Promotion re-derive against.
func TestFloorTracksPricingAndRoutingDrift(t *testing.T) {
	reg, routes := productionWorld()
	before, _ := newProvider(t, reg, routes, productionPricing(), nil)
	a, err := before.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}

	repriced := productionPricing()
	for i := range repriced.tiers {
		if repriced.tiers[i].ProviderID == "gemini" {
			repriced.tiers[i].OutputPriceNanosPerMillion *= 2
		}
	}
	afterPrice, _ := newProvider(t, reg, routes, repriced, nil)
	b, err := afterPrice.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if b.MinUSD <= a.MinUSD {
		t.Fatalf("doubling the leader model's output price must raise the floor: %s -> %s", a.MinUSD, b.MinUSD)
	}

	// Routing: every leader and worker now routes to the pricey model.
	for _, id := range []string{"ingenieria_ia/orquestador", "negocio/orquestador", "ingenieria_ia/backend", "negocio/analista"} {
		routes.static[id] = modelruntime.RoutableModel{ProviderID: "xai", ProviderModelID: "grok-4.6"}
	}
	afterRoute, _ := newProvider(t, reg, routes, productionPricing(), nil)
	c, err := afterRoute.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if c.MinUSD <= a.MinUSD {
		t.Fatalf("re-routing leaders/workers to a pricier model must raise the floor: %s -> %s", a.MinUSD, c.MinUSD)
	}
}

// A pool policy can select ANY of its candidates at dispatch time, so the floor
// covers the most expensive one.
func TestPoolPolicyCoversItsMostExpensiveCandidate(t *testing.T) {
	reg, routes := productionWorld()
	routes.pools = map[string][]modelruntime.RoutableModel{
		"department.leader": {
			{ProviderID: "gemini", ProviderModelID: "gemini-3.5-flash-lite"},
			{ProviderID: "xai", ProviderModelID: "grok-4.6"},
		},
	}
	provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
	got, err := provider.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, stage := range got.Basis.Stages {
		if stage.Stage == "department_plan" && stage.ProviderID == "xai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the department-plan stage must be sized on the pool's priciest candidate: %+v", got.Basis.Stages)
	}
}

// Fail closed: an unreadable, unroutable or unpriced canonical fact is never
// treated as a free one.
func TestUnderivableFloorsFailClosed(t *testing.T) {
	ctx := context.Background()
	t.Run("no current revision", func(t *testing.T) {
		reg, routes := productionWorld()
		reg.revision = nil
		provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
		if _, err := provider.ExecutionBudgetRequirements(ctx, org); err == nil {
			t.Fatal("no revision must fail")
		}
	})
	t.Run("registry error", func(t *testing.T) {
		reg, routes := productionWorld()
		reg.err = errors.New("database down")
		provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
		if _, err := provider.ExecutionBudgetRequirements(ctx, org); err == nil {
			t.Fatal("a registry error must fail")
		}
	})
	t.Run("a stage no role can route", func(t *testing.T) {
		reg, routes := productionWorld()
		for _, id := range []string{"ingenieria_ia/orquestador", "negocio/orquestador"} {
			delete(routes.static, id)
		}
		provider, _ := newProvider(t, reg, routes, productionPricing(), nil)
		_, err := provider.ExecutionBudgetRequirements(ctx, org)
		if err == nil || !strings.Contains(err.Error(), "department_plan") {
			t.Fatalf("no routable leader must fail naming the stage, got: %v", err)
		}
	})
	t.Run("a reachable route has no price", func(t *testing.T) {
		reg, routes := productionWorld()
		var kept []modelpricing.PriceTier
		for _, tier := range productionPricing().tiers {
			if tier.ProviderID != "gemini" {
				kept = append(kept, tier)
			}
		}
		provider, _ := newProvider(t, reg, routes, &memoryPricing{tiers: kept}, nil)
		if _, err := provider.ExecutionBudgetRequirements(ctx, org); err == nil {
			t.Fatal("an unpriced route real dispatch could not reserve must fail the floor")
		}
	})
	t.Run("construction guards", func(t *testing.T) {
		reg, routes := productionWorld()
		service, _ := modelpricing.NewService(productionPricing())
		gate, _ := costgate.New(service, &amountLedger{}, &amountBudgets{})
		good := executionrequirements.Config{Registry: reg, Routes: routes, Costs: gate, Limits: executive.DefaultLimits(), ContextMaxTotalBytes: 1}
		for name, mutate := range map[string]func(*executionrequirements.Config){
			"no registry":   func(c *executionrequirements.Config) { c.Registry = nil },
			"no routes":     func(c *executionrequirements.Config) { c.Routes = nil },
			"no costs":      func(c *executionrequirements.Config) { c.Costs = nil },
			"no context":    func(c *executionrequirements.Config) { c.ContextMaxTotalBytes = 0 },
			"no output cap": func(c *executionrequirements.Config) { c.Limits.MaxOutputTokens = 0 },
		} {
			cfg := good
			mutate(&cfg)
			if _, err := executionrequirements.New(cfg); err == nil {
				t.Errorf("%s must be refused", name)
			}
		}
	})
}

// A subscription-only route reserves $0, but the floor stays strictly positive
// so the AgentBudget stays valid.
func TestSubscriptionOnlyRoutesKeepAPositiveUSDFloor(t *testing.T) {
	reg, routes := productionWorld()
	sub := modelruntime.RoutableModel{ProviderID: "cloudflare_workers_ai", ProviderModelID: "glm"}
	for id := range routes.static {
		routes.static[id] = sub
	}
	service, _ := modelpricing.NewService(&memoryPricing{})
	gate, _ := costgate.New(service, &amountLedger{}, &amountBudgets{}, "cloudflare_workers_ai")
	provider, err := executionrequirements.New(executionrequirements.Config{
		Registry: reg, Routes: routes, Costs: gate, Limits: executive.DefaultLimits(), ContextMaxTotalBytes: 524288,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := provider.ExecutionBudgetRequirements(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if got.MinUSD != 1 || got.Validate() != nil {
		t.Fatalf("subscription-only floor = %+v", got)
	}
}
