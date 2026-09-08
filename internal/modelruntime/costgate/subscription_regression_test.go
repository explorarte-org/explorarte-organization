package costgate

// Regression tests demanded before canonical approval of the Cloudflare
// Workers AI integration (review round 2026-09-08, blockers B1):
//
//  1. A quota/subscription-billed provider invocation must NOT require a
//     PriceTier, must NOT reserve USD against provider_wallets, must report
//     WalletApplied=false and USD usage of exactly zero — while model-call
//     and token usage ARE still counted against any attached budget, and
//     the reservation is returned so the provider call proceeds.
//
//  2. A subscription provider can never accidentally fall into PAYG even
//     if a positive price tier exists for it: the subscription set wins
//     before pricing resolution is ever consulted.
//
// These tests are intentionally test-only: they gate the approval decision
// and travel with the READY_FOR_HUMAN_APPROVAL package.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// recordingLedger captures every wallet mutation. Any Reserve/Reconcile
// call in a subscription scenario is a failure of the property under test.
// The embedded nil interface turns any unstubbed method into a loud panic
// instead of a silent fake success.
type recordingLedger struct {
	costledger.Ledger
	reserved  []string
	reconcile []string
	released  []string
}

func (l *recordingLedger) GetWallet(_ context.Context, providerID string) (costledger.ProviderWallet, error) {
	return costledger.ProviderWallet{ProviderID: providerID, BalanceUSD: 1_000_000_000}, nil
}
func (l *recordingLedger) SetBalance(context.Context, string, modelpricing.USDNanos, time.Time) (costledger.ProviderWallet, error) {
	return costledger.ProviderWallet{}, nil
}
func (l *recordingLedger) Reserve(ctx context.Context, providerID string, _ int64, _ modelpricing.USDNanos, _ time.Time) error {
	l.reserved = append(l.reserved, providerID)
	return nil
}
func (l *recordingLedger) Reconcile(ctx context.Context, providerID string, _ int64, _ modelpricing.USDNanos, _ time.Time) error {
	l.reconcile = append(l.reconcile, providerID)
	return nil
}
func (l *recordingLedger) Release(ctx context.Context, providerID string, _ int64, _ time.Time) error {
	l.released = append(l.released, providerID)
	return nil
}
func (l *recordingLedger) ListEvents(context.Context, string, int) ([]costledger.WalletEvent, error) {
	return nil, nil
}
func (l *recordingLedger) ListOrphanedReservations(context.Context, time.Time, int) ([]costledger.WalletEvent, error) {
	return nil, nil
}

var _ costledger.Ledger = (*recordingLedger)(nil)

// recordingBudgets captures budget consumption to prove token/model-call
// usage stays counted for subscription providers.
type recordingBudgets struct {
	agentbudget.Ledger
	consumed []agentbudget.Usage
}

func (b *recordingBudgets) ResolveBudgetForTask(context.Context, int64) (agentbudget.Budget, error) {
	return agentbudget.Budget{ID: 7}, nil
}
func (b *recordingBudgets) ConsumeModelCall(ctx context.Context, budgetID, invocationID int64, delta agentbudget.Usage, now time.Time) error {
	b.consumed = append(b.consumed, delta)
	return nil
}

var _ agentbudget.Ledger = (*recordingBudgets)(nil)

// sentinelPricingStore backs a REAL modelpricing.Service whose only job is
// to explode if the gate ever consults pricing for a subscription provider.
type sentinelPricingStore struct{ consulted bool }

func (s *sentinelPricingStore) ListTiers(context.Context, string, string, modelpricing.BillingMode, time.Time) ([]modelpricing.PriceTier, error) {
	s.consulted = true
	return nil, errors.New("pricing must never be consulted for subscription providers")
}
func (s *sentinelPricingStore) Upsert(_ context.Context, tier modelpricing.PriceTier) (modelpricing.PriceTier, error) {
	s.consulted = true
	return tier, nil
}

func newSubscriptionGate(t *testing.T) (*Gate, *recordingLedger, *recordingBudgets, *sentinelPricingStore) {
	t.Helper()
	ledger := &recordingLedger{}
	budgets := &recordingBudgets{}
	store := &sentinelPricingStore{}
	pricing, err := modelpricing.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := New(pricing, ledger, budgets, "cloudflare_workers_ai")
	if err != nil {
		t.Fatal(err)
	}
	return gate, ledger, budgets, store
}

func TestSubscriptionProvider_NeverPAYG(t *testing.T) {
	gate, ledger, budgets, pricing := newSubscriptionGate(t)
	reservation, err := gate.Reserve(context.Background(), modelruntime.CostReservationRequest{
		ProviderID:           "cloudflare_workers_ai",
		ProviderModelID:      "@cf/zai-org/glm-4.7-flash",
		InvocationID:         1,
		TaskID:               42,
		EstimatedInputTokens: 500,
		MaxOutputTokens:      200,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("subscription reservation must succeed without pricing/wallet: %v", err)
	}

	// The reviewer's exact assertions:
	if reservation.WalletApplied {
		t.Fatal("WalletApplied must be false — no USD wallet reservation")
	}
	if !reservation.Subscription {
		t.Fatal("reservation must be marked Subscription")
	}
	if !reservation.BudgetApplied {
		t.Fatal("budget application (token/model-call dimension) must still happen")
	}
	if len(ledger.reserved) != 0 || len(ledger.reconcile) != 0 || len(ledger.released) != 0 {
		t.Fatalf("wallet ledger must be untouched: %+v", ledger)
	}
	if pricing.consulted {
		t.Fatal("pricing must never be consulted for a subscription provider — even if a positive tier existed")
	}
	// Model-call/token usage STILL counted (USD dimension honestly zero):
	if len(budgets.consumed) != 1 {
		t.Fatalf("budget consumption must still happen, got %+v", budgets.consumed)
	}
	if budgets.consumed[0].UsedUSD != 0 {
		t.Fatalf("budget USD must be zero for subscription billing, got %v", budgets.consumed[0].UsedUSD)
	}
	if budgets.consumed[0].UsedTokens != 700 {
		t.Fatalf("token usage must be counted: %v", budgets.consumed[0].UsedTokens)
	}
	if budgets.consumed[0].UsedModelCalls != 1 {
		t.Fatalf("model-call usage must be counted: %v", budgets.consumed[0].UsedModelCalls)
	}
}

// staticTierStore serves the canonical ministral-8b-latest Standard tier
// ($0.15/1M in and out => 150000 nanos/1M) — official pricing, provenance
// documented in migration 000069.
type staticTierStore struct{}

func (s *staticTierStore) ListTiers(_ context.Context, providerID, modelID string, mode modelpricing.BillingMode, _ time.Time) ([]modelpricing.PriceTier, error) {
	if providerID != "mistral" || modelID != "ministral-8b-latest" {
		return nil, nil
	}
	return []modelpricing.PriceTier{{
		ProviderID: providerID, ProviderModelID: modelID, ContextTierName: "standard",
		InputPriceNanosPerMillion: 150000, OutputPriceNanosPerMillion: 150000,
		BillingMode: mode, EffectiveAt: time.Now().UTC(),
	}}, nil
}
func (s *staticTierStore) Upsert(_ context.Context, tier modelpricing.PriceTier) (modelpricing.PriceTier, error) {
	return tier, nil
}

// TestMistralIsNotASubscriptionProvider locks the economic boundary: the
// Mistral provider goes through the NORMAL pricing + reservation path
// (finite monthly included usage, PAYG possibly enabled upstream), so it
// must NEVER join costgate subscriptionProviders — unlike Cloudflare.
// With mistral priced and a wallet present, Reserve must consult pricing,
// reserve against the wallet, and return WalletApplied=true.
func TestMistralIsNotASubscriptionProvider(t *testing.T) {
	ledger := &recordingLedger{}
	budgets := &recordingBudgets{consumed: []agentbudget.Usage{}}
	pricing, err := modelpricing.NewService(&staticTierStore{})
	if err != nil {
		t.Fatal(err)
	}
	// NOTE: "mistral" deliberately NOT passed as a subscription provider.
	gate, err := New(pricing, ledger, budgets)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := gate.Reserve(context.Background(), modelruntime.CostReservationRequest{
		ProviderID: "mistral", ProviderModelID: "ministral-8b-latest",
		InvocationID: 9, TaskID: 42, EstimatedInputTokens: 28, MaxOutputTokens: 10,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("priced mistral reservation must succeed: %v", err)
	}
	if reservation.Subscription {
		t.Fatal("mistral must never be billed as a zero-cost subscription")
	}
	if !reservation.WalletApplied || len(ledger.reserved) != 1 || ledger.reserved[0] != "mistral" {
		t.Fatalf("mistral must reserve against the wallet: %+v", ledger)
	}
}
