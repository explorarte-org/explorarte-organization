//go:build integration

package executive_test

import (
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
)

// The campaign budget is only genuinely single-source if the DURABLE row
// carries what the submission stated. Everything upstream of that row is a
// claim; the row is the thing every later spending decision reads, and it is
// written ON CONFLICT (task_id) DO NOTHING, so whatever lands there first is
// the campaign's budget for good.
//
// A unit test can show that Submit passed the right value along. Only this can
// show it arrived.
func TestCampaignBudgetIsDurableFromBirthPostgreSQL(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	budgetLedger, err := agentbudgetpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, h.completion,
		executive.WithAgentBudgets(runtimeadapter.AgentBudgets{Ledger: budgetLedger}))

	stated := executive.DefaultCampaignBudget()
	stated.MaxUSD = modelpricing.USDFromDollars(17)
	stated.MaxTokens = 120_000_000

	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID:    executive.OwnerRoleID,
		IdempotencyKey: "integration-campaign-budget",
		Goal: executive.OwnerGoal{
			Goal:               "Analyze one area.",
			AcceptanceCriteria: []string{"verified"},
		},
		Budget: &stated,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	var maxUSDNanos, maxTokens int64
	if err = h.store.Pool().QueryRow(h.ctx,
		`SELECT max_usd_nanos,max_tokens FROM agent_budgets WHERE task_id=$1 AND parent_budget_id IS NULL`,
		run.RootTaskID,
	).Scan(&maxUSDNanos, &maxTokens); err != nil {
		t.Fatalf("read the campaign's durable budget: %v", err)
	}
	if modelpricing.USDNanos(maxUSDNanos) != stated.MaxUSD || maxTokens != stated.MaxTokens {
		t.Fatalf("the durable campaign budget is not what the submission stated: got %s/%d tokens, want %s/%d",
			modelpricing.USDNanos(maxUSDNanos), maxTokens, stated.MaxUSD, stated.MaxTokens)
	}

	// This is the incident that produced this fix: a campaign born with the
	// package defaults because the submitting process had no ceilings
	// configured, while the runtime driving it had been given others.
	if modelpricing.USDNanos(maxUSDNanos) == executive.DefaultCampaignBudget().MaxUSD {
		t.Fatal("the campaign was born with the default ceiling despite stating its own")
	}
}

// Resubmitting a campaign under its own key must not be able to restate its
// budget. The task is reused by idempotency key, so without a check at the
// ledger the second Submit reported success while the durable budget kept the
// original terms -- the caller believed it had set a ceiling the system had
// quietly refused to change. That is the same "two answers, no complaint"
// shape this branch exists to remove.
func TestResubmittingCannotRestateACampaignBudgetPostgreSQL(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()

	models := newIntegrationModelRuntime()
	budgetLedger, err := agentbudgetpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, h.completion,
		executive.WithAgentBudgets(runtimeadapter.AgentBudgets{Ledger: budgetLedger}))

	stated := executive.DefaultCampaignBudget()
	stated.MaxUSD = modelpricing.USDFromDollars(17)
	submit := func(budget executive.CampaignBudget) error {
		_, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
			ActorRoleID:    executive.OwnerRoleID,
			IdempotencyKey: "integration-campaign-budget-restate",
			Goal: executive.OwnerGoal{
				Goal:               "Analyze one area.",
				AcceptanceCriteria: []string{"verified"},
			},
			Budget: &budget,
		})
		return err
	}

	if err = submit(stated); err != nil {
		t.Fatalf("first submission: %v", err)
	}
	if err = submit(stated); err != nil {
		t.Fatalf("resubmitting the same campaign with the same budget must stay idempotent: %v", err)
	}

	lowered := stated
	lowered.MaxUSD = modelpricing.USDFromDollars(5)
	if err = submit(lowered); !errors.Is(err, agentbudget.ErrBudgetConflict) {
		t.Fatalf("restating a campaign budget must fail closed, got %v", err)
	}

	var maxUSDNanos int64
	if err = h.store.Pool().QueryRow(h.ctx,
		`SELECT ab.max_usd_nanos FROM agent_budgets ab
		 JOIN tasks t ON t.id = ab.task_id
		 WHERE t.idempotency_key = $1 AND ab.parent_budget_id IS NULL`,
		"integration-campaign-budget-restate",
	).Scan(&maxUSDNanos); err != nil {
		t.Fatal(err)
	}
	if modelpricing.USDNanos(maxUSDNanos) != stated.MaxUSD {
		t.Fatalf("the refused resubmission changed the durable campaign budget to %s", modelpricing.USDNanos(maxUSDNanos))
	}
}
