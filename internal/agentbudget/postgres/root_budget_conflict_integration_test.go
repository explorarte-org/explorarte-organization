//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// Idempotence means asking twice for the SAME thing changes nothing. It does
// not mean asking twice succeeds. The distinction is only observable against
// the real ON CONFLICT DO NOTHING, which is why this is an integration test:
// the database is what silently refuses the second write, and the bug was
// that nobody looked at what it had refused.
func TestCreateRootBudgetRejectsContradictoryReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// Every dimension gets its own case. A hand-written comparison that
	// forgot one would let that dimension be silently overridden, and a
	// forgotten dimension is exactly the kind of thing that is never noticed
	// until a campaign runs under a ceiling nobody chose.
	original := testLimits()
	for _, tc := range []struct {
		dimension string
		mutate    func(*agentbudget.Limits)
	}{
		{"MaxUSD", func(l *agentbudget.Limits) { l.MaxUSD = modelpricing.USDFromDollars(17) }},
		{"MaxTokens", func(l *agentbudget.Limits) { l.MaxTokens = 120_000_000 }},
		{"MaxModelCalls", func(l *agentbudget.Limits) { l.MaxModelCalls = 999 }},
		{"MaxWallTimeMS", func(l *agentbudget.Limits) { l.MaxWallTimeMS = 7_200_000 }},
		{"MaxDepth", func(l *agentbudget.Limits) { l.MaxDepth = 9 }},
		{"MaxRetries", func(l *agentbudget.Limits) { l.MaxRetries = 99 }},
		{"MaxSubagents", func(l *agentbudget.Limits) { l.MaxSubagents = 77 }},
	} {
		t.Run(tc.dimension, func(t *testing.T) {
			rootTaskID := fixture.insertTask(t, ctx, budgetIntegrationRole)
			created, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, budgetIntegrationRole, original, now)
			if err != nil {
				t.Fatal(err)
			}

			// Same terms: still idempotent, still one row, unchanged.
			again, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, budgetIntegrationRole, original, now)
			if err != nil {
				t.Fatalf("an identical request must remain idempotent: %v", err)
			}
			if again.ID != created.ID || again.Limits != original {
				t.Fatalf("idempotent reuse changed the budget: %+v", again)
			}

			contradictory := original
			tc.mutate(&contradictory)
			_, err = ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, budgetIntegrationRole, contradictory, now)
			if !errors.Is(err, agentbudget.ErrBudgetConflict) {
				t.Fatalf("a retry stating a different %s must fail closed, got %v", tc.dimension, err)
			}

			// The durable budget is what matters: a refusal that had already
			// rewritten the row would be worse than a silent success.
			after, err := ledger.ResolveBudgetForTask(ctx, rootTaskID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Limits != original {
				t.Fatalf("the refused request changed the durable budget: %+v", after.Limits)
			}
			var rows int
			if err = fixture.store.Pool().QueryRow(ctx, `SELECT count(*) FROM agent_budgets WHERE task_id=$1`, rootTaskID).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if rows != 1 {
				t.Fatalf("want exactly one root budget row, got %d", rows)
			}
		})
	}
}

// The durable row also owns an identity, not only ceilings. A caller that
// agrees on all seven numbers but names a different owning role is not
// describing the same budget, and saying "success" would attach a campaign to
// a role that never had it.
func TestCreateRootBudgetRejectsContradictoryIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, budgetIntegrationRole)
	if _, err = ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, budgetIntegrationRole, testLimits(), now); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", testLimits(), now); !errors.Is(err, agentbudget.ErrBudgetConflict) {
		t.Fatalf("a different owning role must fail closed, got %v", err)
	}
	after, err := ledger.ResolveBudgetForTask(ctx, rootTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if after.RoleID != budgetIntegrationRole {
		t.Fatalf("the refused request changed the durable owner: %q", after.RoleID)
	}
}
