//go:build integration

package executive_test

import (
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// These tests run the REAL orchestrator against the REAL PostgreSQL AgentBudget
// ledger (no fake ledger) to prove the topology floor Campaign derives from
// executive.MinimalCampaignTopology is what the ledger actually enforces:
//
//   - production's recommended budget (1 subagent, depth 2) cannot carry the
//     minimal campaign -- the first child spends the one subagent and a depth-3
//     worker is refused;
//   - a budget exactly at the topology minima carries it to a completed root,
//     and the ledger ends with exactly the minima consumed;
//   - one below either minimum stops the run.
//
// USD/tokens/model calls are not consumed by this harness's fake Model Runtime
// (CostGate is what charges them in production; its parity is proven in
// internal/modelruntime/costgate and internal/campaign/executionrequirements),
// so they are set generously here and only the structural dimensions are the
// variable under test.

type topologyBudget struct {
	maxDepth, maxSubagents int64
}

func topologyLimits(b topologyBudget) executive.CampaignBudget {
	return agentbudget.Limits{
		MaxUSD: modelpricing.USDFromDollars(1000), MaxTokens: 50_000_000, MaxModelCalls: 1000,
		MaxWallTimeMS: 3_600_000, MaxDepth: b.maxDepth, MaxRetries: 1, MaxSubagents: b.maxSubagents,
	}
}

type topologyOutcome struct {
	run                      executive.Run
	err                      error
	usedSubagents, usedDepth int64
	children                 int
}

func runMinimalCampaign(t *testing.T, key string, budget executive.CampaignBudget) topologyOutcome {
	t.Helper()
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	ledger, err := agentbudgetpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, h.completion,
		executive.WithAgentBudgets(runtimeadapter.AgentBudgets{Ledger: ledger}))
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: key, Budget: &budget,
		Goal: executive.OwnerGoal{
			Goal:               "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{{Text: "one department reviewed", Phase: executive.AcceptanceDesign}, {Text: "closure verified", Phase: executive.AcceptanceImplementation}},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	outcome := topologyOutcome{}
	outcome.run, outcome.err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 30)
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT used_subagents, depth FROM agent_budgets WHERE task_id=$1 AND parent_budget_id IS NULL`, run.RootTaskID,
	).Scan(&outcome.usedSubagents, &outcome.usedDepth); err != nil {
		t.Fatalf("read the campaign's durable budget usage: %v", err)
	}
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT count(*) FROM task_budgets tb JOIN agent_budgets ab ON ab.id=tb.budget_id WHERE ab.task_id=$1 AND tb.task_id<>$1`, run.RootTaskID,
	).Scan(&outcome.children); err != nil {
		t.Fatalf("count children attached to the campaign budget: %v", err)
	}
	return outcome
}

func (o topologyOutcome) failureText() string {
	text := ""
	if o.err != nil {
		text += o.err.Error() + " "
	}
	text += o.run.Reason + " " + o.run.ReasonCode
	return text
}

// EXACT MINIMA: the derived topology floor carries the minimal campaign all the
// way to a completed root, and consumes exactly that floor.
func TestTopologyFloorCarriesTheMinimalCampaignOnTheRealLedgerPostgreSQL(t *testing.T) {
	floor := executive.MinimalCampaignTopology()
	outcome := runMinimalCampaign(t, "topology-exact-minima", topologyLimits(topologyBudget{maxDepth: floor.Depth, maxSubagents: floor.Subagents}))
	if outcome.err != nil || outcome.run.State != executive.StateCompleted {
		t.Fatalf("a budget exactly at the topology floor (depth %d, subagents %d) must complete the minimal campaign: run=%+v err=%v",
			floor.Depth, floor.Subagents, outcome.run, outcome.err)
	}
	if outcome.usedSubagents != floor.Subagents || outcome.children != int(floor.Subagents) {
		t.Fatalf("the minimal campaign attached %d children (ledger says %d subagents used); Executive's declaration says %d",
			outcome.children, outcome.usedSubagents, floor.Subagents)
	}
	if outcome.usedDepth != floor.Depth {
		t.Fatalf("the deepest child attached at depth %d; Executive's declaration says %d", outcome.usedDepth, floor.Depth)
	}
}

// PRODUCTION'S BUDGET: max_subagents=1 is exhausted by the very first attached
// child (the CEO plan), so nothing beyond it can ever be attached; and
// max_depth=2 could not have reached a depth-3 worker in any case.
func TestProductionBudgetCannotCarryTheMinimalCampaignOnTheRealLedgerPostgreSQL(t *testing.T) {
	outcome := runMinimalCampaign(t, "topology-production-shape", topologyLimits(topologyBudget{maxDepth: 2, maxSubagents: 1}))
	if outcome.run.State == executive.StateCompleted {
		t.Fatalf("production's structural budget (depth 2, subagents 1) must NOT complete the minimal campaign: %+v", outcome.run)
	}
	if outcome.usedSubagents != 1 {
		t.Fatalf("max_subagents=1 must be spent by the first attached child, ledger used %d", outcome.usedSubagents)
	}
	if !strings.Contains(outcome.failureText(), "subagent count would exceed max 1") {
		t.Fatalf("the run must stop on the second child's attachment (subagent exhaustion); got: %q", outcome.failureText())
	}
}

func TestDepthTwoRefusesADepthThreeWorkerOnTheRealLedgerPostgreSQL(t *testing.T) {
	floor := executive.MinimalCampaignTopology()
	// Subagents are ample; ONLY depth is short by one.
	outcome := runMinimalCampaign(t, "topology-depth-short", topologyLimits(topologyBudget{maxDepth: floor.Depth - 1, maxSubagents: floor.Subagents + 10}))
	if outcome.run.State == executive.StateCompleted {
		t.Fatalf("depth %d must not complete a campaign whose worker sits at depth %d", floor.Depth-1, floor.Depth)
	}
	if !strings.Contains(outcome.failureText(), "child depth 3 exceeds max 2") {
		t.Fatalf("the run must stop at the depth-3 worker; got: %q", outcome.failureText())
	}
	// The CEO plan and department plan attached; the worker did not.
	if outcome.usedSubagents != 2 {
		t.Fatalf("subagents used before the depth refusal = %d, want 2 (CEO plan, department plan)", outcome.usedSubagents)
	}
}

func TestOneSubagentShortStopsTheMinimalCampaignOnTheRealLedgerPostgreSQL(t *testing.T) {
	floor := executive.MinimalCampaignTopology()
	outcome := runMinimalCampaign(t, "topology-subagents-short", topologyLimits(topologyBudget{maxDepth: floor.Depth, maxSubagents: floor.Subagents - 1}))
	if outcome.run.State == executive.StateCompleted {
		t.Fatalf("subagents %d must not complete a campaign that attaches %d children", floor.Subagents-1, floor.Subagents)
	}
	if outcome.usedSubagents != floor.Subagents-1 {
		t.Fatalf("subagents used = %d, want the whole short budget (%d) consumed", outcome.usedSubagents, floor.Subagents-1)
	}
}
