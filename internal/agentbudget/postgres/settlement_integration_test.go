//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// AUTONOMY-SMOKE-017-R4 reserved 1,024,000 output tokens across eight calls,
// emitted 36,753, and died of exhaustion with 69% of its ceiling spent on
// output space it never used. A reservation that is never settled is not a
// budget, it is a toll.

func TestAnUnusedOutputReservationIsGivenBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	limits := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(10), MaxTokens: 200_000, MaxModelCalls: 4, MaxWallTimeMS: 600_000, MaxDepth: 3, MaxRetries: 2, MaxSubagents: 1}
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", limits, now)
	if err != nil {
		t.Fatal(err)
	}

	// What the gate charges before the call: the estimate plus the whole
	// output allowance.
	const estimatedInput, maxOutput = 60_000, 128_000
	charged := agentbudget.Usage{UsedUSD: modelpricing.USDFromDollars(1), UsedTokens: estimatedInput + maxOutput, UsedModelCalls: 1}
	if err = ledger.ConsumeModelCall(ctx, root.ID, 501, charged, now); err != nil {
		t.Fatal(err)
	}

	// What the provider reported afterwards.
	const actualInput, actualOutput = 114_907, 15_970
	actual := agentbudget.Usage{UsedUSD: modelpricing.USDFromDollars(2), UsedTokens: actualInput + actualOutput, UsedModelCalls: 1}
	if err = ledger.SettleModelCall(ctx, root.ID, 501, actual, now); err != nil {
		t.Fatal(err)
	}

	settled, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Usage.UsedTokens != actualInput+actualOutput {
		t.Fatalf("the account still holds the reservation: used=%d want %d",
			settled.Usage.UsedTokens, actualInput+actualOutput)
	}
	if settled.Usage.UsedUSD != modelpricing.USDFromDollars(2) {
		t.Fatalf("USD was not settled: used=%v", settled.Usage.UsedUSD)
	}
	// One admitted call stays one call, whatever it produced.
	if settled.Usage.UsedModelCalls != 1 {
		t.Fatalf("settling changed the call count: %d", settled.Usage.UsedModelCalls)
	}
}

// A settlement applied twice must move the account once.
func TestSettlingTwiceMovesTheAccountOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	limits := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(10), MaxTokens: 200_000, MaxModelCalls: 4, MaxWallTimeMS: 600_000, MaxDepth: 3, MaxRetries: 2, MaxSubagents: 1}
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", limits, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.ConsumeModelCall(ctx, root.ID, 502,
		agentbudget.Usage{UsedUSD: modelpricing.USDFromDollars(1), UsedTokens: 188_000, UsedModelCalls: 1}, now); err != nil {
		t.Fatal(err)
	}
	actual := agentbudget.Usage{UsedUSD: modelpricing.USDFromDollars(1), UsedTokens: 30_000, UsedModelCalls: 1}
	for range 3 {
		if err = ledger.SettleModelCall(ctx, root.ID, 502, actual, now); err != nil {
			t.Fatal(err)
		}
	}
	settled, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Usage.UsedTokens != 30_000 {
		t.Fatalf("repeated settlement moved the account more than once: used=%d", settled.Usage.UsedTokens)
	}
}

// A call can fail before it is ever admitted, and settling it is not an error.
func TestSettlingACallThatWasNeverChargedChangesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	limits := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(10), MaxTokens: 200_000, MaxModelCalls: 4, MaxWallTimeMS: 600_000, MaxDepth: 3, MaxRetries: 2, MaxSubagents: 1}
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", limits, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.SettleModelCall(ctx, root.ID,
		9_999, agentbudget.Usage{UsedTokens: 50_000, UsedModelCalls: 1}, now); err != nil {
		t.Fatalf("settling an unadmitted call must be a no-op, not an error: %v", err)
	}
	settled, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Usage.UsedTokens != 0 {
		t.Fatalf("a settlement invented a charge that was never made: used=%d", settled.Usage.UsedTokens)
	}
}
