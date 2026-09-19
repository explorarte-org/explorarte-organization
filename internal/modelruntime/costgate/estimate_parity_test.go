package costgate

import (
	"context"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// memoryPricing is an in-memory rate card holding production's real
// openai_responses/gpt-5.6-luna tiers (default and long_context).
type memoryPricing struct{ tiers []modelpricing.PriceTier }

func (m memoryPricing) ListTiers(_ context.Context, providerID, providerModelID string, mode modelpricing.BillingMode, _ time.Time) ([]modelpricing.PriceTier, error) {
	var out []modelpricing.PriceTier
	for _, tier := range m.tiers {
		if tier.ProviderID == providerID && tier.ProviderModelID == providerModelID && tier.BillingMode == mode {
			out = append(out, tier)
		}
	}
	return out, nil
}
func (m memoryPricing) Upsert(_ context.Context, tier modelpricing.PriceTier) (modelpricing.PriceTier, error) {
	return tier, nil
}

func productionLunaTiers() []modelpricing.PriceTier {
	at := time.Date(2026, 9, 5, 1, 19, 33, 0, time.UTC)
	return []modelpricing.PriceTier{
		{ProviderID: "openai_responses", ProviderModelID: "gpt-5.6-luna", ContextTierName: "default", MinInputTokens: 0,
			InputPriceNanosPerMillion: 200_000_000, OutputPriceNanosPerMillion: 1_200_000_000, BillingMode: modelpricing.BillingOnline, EffectiveAt: at},
		{ProviderID: "openai_responses", ProviderModelID: "gpt-5.6-luna", ContextTierName: "long_context", MinInputTokens: 272_000,
			InputPriceNanosPerMillion: 400_000_000, OutputPriceNanosPerMillion: 1_800_000_000, BillingMode: modelpricing.BillingOnline, EffectiveAt: at},
	}
}

// amountLedger and amountBudgets record what a REAL Reserve charges.
type amountLedger struct {
	costledger.Ledger
	reservedUSD []modelpricing.USDNanos
}

func (l *amountLedger) Reserve(_ context.Context, _ string, _ int64, usd modelpricing.USDNanos, _ time.Time) error {
	l.reservedUSD = append(l.reservedUSD, usd)
	return nil
}

type amountBudgets struct {
	agentbudget.Ledger
	consumed []agentbudget.Usage
}

func (b *amountBudgets) ResolveBudgetForTask(context.Context, int64) (agentbudget.Budget, error) {
	return agentbudget.Budget{ID: 1}, nil
}
func (b *amountBudgets) ConsumeModelCall(_ context.Context, _, _ int64, delta agentbudget.Usage, _ time.Time) error {
	b.consumed = append(b.consumed, delta)
	return nil
}

// The read-only estimate a host preflight uses and the reservation real
// dispatch makes must be the SAME number for the same route, price and token
// inputs -- exactly, in modelpricing.USDNanos, across the long-context tier
// boundary and for both output ceilings Executive uses. Reserve is built on
// EstimateReservation, so this pins that they can never diverge.
func TestEstimateReservationIsExactlyWhatReserveCharges(t *testing.T) {
	pricing, err := modelpricing.NewService(memoryPricing{tiers: productionLunaTiers()})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name            string
		input, output   int64
		wantUSD         modelpricing.USDNanos
		wantTier        string
		checkExactNanos bool
	}{
		{"production first CEO-plan dispatch", 33_268, 128_000, 160_253_600, "default", true},
		{"worker output ceiling", 33_268, 24_000, 33_268*200_000_000/1_000_000 + 24_000*1_200_000_000/1_000_000, "default", true},
		{"just below the long-context threshold", 271_999, 128_000, 0, "default", false},
		{"exactly at the long-context threshold", 272_000, 128_000, 272_000*400_000_000/1_000_000 + 128_000*1_800_000_000/1_000_000, "long_context", true},
		{"far above the threshold", 349_000, 128_000, 349_000*400_000_000/1_000_000 + 128_000*1_800_000_000/1_000_000, "long_context", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger, budgets := &amountLedger{}, &amountBudgets{}
			gate, err := New(pricing, ledger, budgets)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
			estimate, err := gate.EstimateReservation(context.Background(), modelruntime.ReservationEstimateRequest{
				ProviderID: "openai_responses", ProviderModelID: "gpt-5.6-luna",
				EstimatedInputTokens: tc.input, MaxOutputTokens: tc.output,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.reservedUSD) != 0 || len(budgets.consumed) != 0 {
				t.Fatal("EstimateReservation must reserve and consume nothing")
			}
			if _, err = gate.Reserve(context.Background(), modelruntime.CostReservationRequest{
				OrganizationID: "explorarte", TaskID: 789, InvocationID: 203,
				ProviderID: "openai_responses", ProviderModelID: "gpt-5.6-luna",
				EstimatedInputTokens: tc.input, MaxOutputTokens: tc.output,
			}, now); err != nil {
				t.Fatal(err)
			}
			if len(ledger.reservedUSD) != 1 || len(budgets.consumed) != 1 {
				t.Fatalf("Reserve made %d wallet reservations and %d budget charges, want 1 and 1", len(ledger.reservedUSD), len(budgets.consumed))
			}
			if int64(ledger.reservedUSD[0]) != estimate.USDNanos {
				t.Fatalf("wallet reservation %d != estimate %d", ledger.reservedUSD[0], estimate.USDNanos)
			}
			if int64(budgets.consumed[0].UsedUSD) != estimate.USDNanos {
				t.Fatalf("AgentBudget USD charge %d != estimate %d", budgets.consumed[0].UsedUSD, estimate.USDNanos)
			}
			if budgets.consumed[0].UsedTokens != estimate.Tokens || estimate.Tokens != tc.input+tc.output {
				t.Fatalf("AgentBudget token charge %d, estimate %d, want input+output %d", budgets.consumed[0].UsedTokens, estimate.Tokens, tc.input+tc.output)
			}
			if estimate.PriceTier != tc.wantTier {
				t.Fatalf("price tier = %q, want %q", estimate.PriceTier, tc.wantTier)
			}
			if tc.checkExactNanos && modelpricing.USDNanos(estimate.USDNanos) != tc.wantUSD {
				t.Fatalf("estimate = %d nanos, want %d", estimate.USDNanos, tc.wantUSD)
			}
		})
	}
}

// A subscription-billed provider has no per-call USD price and never falls
// into pay-as-you-go pricing, in the estimate exactly as in Reserve.
func TestEstimateReservationSubscriptionProviderIsFreeButStillCountsTokens(t *testing.T) {
	pricing, err := modelpricing.NewService(memoryPricing{tiers: productionLunaTiers()})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := New(pricing, &amountLedger{}, &amountBudgets{}, "cloudflare_workers_ai")
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := gate.EstimateReservation(context.Background(), modelruntime.ReservationEstimateRequest{
		ProviderID: "cloudflare_workers_ai", ProviderModelID: "@cf/zai-org/glm-4.7-flash", EstimatedInputTokens: 1000, MaxOutputTokens: 500,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !estimate.Subscription || estimate.USDNanos != 0 || estimate.Tokens != 1500 {
		t.Fatalf("subscription estimate = %+v", estimate)
	}
}

// An unpriced route cannot be reserved by real dispatch, so it must not be
// estimable either: fail closed, never a free reservation.
func TestEstimateReservationFailsClosedWithoutAPrice(t *testing.T) {
	pricing, _ := modelpricing.NewService(memoryPricing{tiers: productionLunaTiers()})
	gate, _ := New(pricing, &amountLedger{}, &amountBudgets{})
	if _, err := gate.EstimateReservation(context.Background(), modelruntime.ReservationEstimateRequest{
		ProviderID: "unpriced", ProviderModelID: "nope", EstimatedInputTokens: 1, MaxOutputTokens: 1,
	}, time.Now()); err == nil {
		t.Fatal("an unpriced route must fail closed")
	}
}
