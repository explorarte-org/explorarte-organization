package modelpricing

import (
	"errors"
	"testing"
	"time"
)

func nanos(n int64) *USDNanos { v := USDNanos(n); return &v }

func TestResolvePicksMostSpecificContextTierBelowThreshold(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	short := PriceTier{ProviderID: "openai_compatible", ProviderModelID: "gpt-5.6-luna", ContextTierName: "default", MinInputTokens: 0, InputPriceNanosPerMillion: 200_000_000, OutputPriceNanosPerMillion: 1_200_000_000, BillingMode: BillingOnline, EffectiveAt: now}
	long := PriceTier{ProviderID: "openai_compatible", ProviderModelID: "gpt-5.6-luna", ContextTierName: "long_context", MinInputTokens: 272_000, InputPriceNanosPerMillion: 400_000_000, OutputPriceNanosPerMillion: 1_800_000_000, BillingMode: BillingOnline, EffectiveAt: now}
	candidates := []PriceTier{short, long}

	got, err := Resolve(candidates, 1_000, BillingOnline)
	if err != nil || got.ContextTierName != "default" {
		t.Fatalf("below threshold: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 271_999, BillingOnline)
	if err != nil || got.ContextTierName != "default" {
		t.Fatalf("just below threshold: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 272_000, BillingOnline)
	if err != nil || got.ContextTierName != "long_context" {
		t.Fatalf("at threshold: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 500_000, BillingOnline)
	if err != nil || got.ContextTierName != "long_context" {
		t.Fatalf("above threshold: got=%+v err=%v", got, err)
	}
}

// TestResolveNeverConflatesOnlineAndBatchAtTheSameThreshold guards the bug a
// second-opinion review of R29's plan caught before any code existed:
// billing_mode is a real pricing dimension, not something ContextTierName
// can stand in for. Two rows sharing ContextTierName="default" and
// MinInputTokens=0 (one priced for a synchronous online call, one for a
// discounted asynchronous Batch API job) must resolve to exactly the row
// matching the requested billing mode, never to whichever one happens to
// win the EffectiveAt tie-break.
func TestResolveNeverConflatesOnlineAndBatchAtTheSameThreshold(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	online := PriceTier{ProviderID: "gemini", ProviderModelID: "gemini-embedding-2", ContextTierName: "default", MinInputTokens: 0, InputPriceNanosPerMillion: 200_000_000, OutputPriceNanosPerMillion: 0, BillingMode: BillingOnline, EffectiveAt: now}
	batch := PriceTier{ProviderID: "gemini", ProviderModelID: "gemini-embedding-2", ContextTierName: "default", MinInputTokens: 0, InputPriceNanosPerMillion: 100_000_000, OutputPriceNanosPerMillion: 0, BillingMode: BillingBatch, EffectiveAt: now}
	candidates := []PriceTier{online, batch}

	got, err := Resolve(candidates, 1_000, BillingOnline)
	if err != nil || got.BillingMode != BillingOnline || got.InputPriceNanosPerMillion != 200_000_000 {
		t.Fatalf("online request: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 1_000, BillingBatch)
	if err != nil || got.BillingMode != BillingBatch || got.InputPriceNanosPerMillion != 100_000_000 {
		t.Fatalf("batch request: got=%+v err=%v", got, err)
	}
	// Order must not matter either — same guarantee as the EffectiveAt
	// tie-break test below, now for billing mode.
	got, err = Resolve([]PriceTier{batch, online}, 1_000, BillingOnline)
	if err != nil || got.BillingMode != BillingOnline {
		t.Fatalf("batch-first order, online request: got=%+v err=%v", got, err)
	}
}

func TestResolveFailsClosedOnInvalidBillingMode(t *testing.T) {
	now := time.Now()
	candidates := []PriceTier{{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default", InputPriceNanosPerMillion: 1, OutputPriceNanosPerMillion: 1, BillingMode: BillingOnline, EffectiveAt: now}}
	if _, err := Resolve(candidates, 100, BillingMode("weekly")); !errors.Is(err, ErrInvalidPriceTier) {
		t.Fatalf("err=%v want ErrInvalidPriceTier", err)
	}
}

func TestResolveBreaksSameThresholdTiesByLatestEffectiveAt(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	stale := PriceTier{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default", MinInputTokens: 0, InputPriceNanosPerMillion: 100, OutputPriceNanosPerMillion: 100, BillingMode: BillingOnline, EffectiveAt: older}
	current := PriceTier{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default", MinInputTokens: 0, InputPriceNanosPerMillion: 999, OutputPriceNanosPerMillion: 999, BillingMode: BillingOnline, EffectiveAt: newer}

	// Both orderings must resolve to the same (newer) row: which one wins
	// must never depend on the slice/SQL row order the caller happened to
	// get back for this immutable, append-only price history.
	got, err := Resolve([]PriceTier{stale, current}, 10, BillingOnline)
	if err != nil || got.InputPriceNanosPerMillion != 999 {
		t.Fatalf("stale-first order: got=%+v err=%v", got, err)
	}
	got, err = Resolve([]PriceTier{current, stale}, 10, BillingOnline)
	if err != nil || got.InputPriceNanosPerMillion != 999 {
		t.Fatalf("current-first order: got=%+v err=%v", got, err)
	}
}

func TestResolveFailsClosedWithoutAnyTier(t *testing.T) {
	if _, err := Resolve(nil, 100, BillingOnline); !errors.Is(err, ErrNoPricingResolved) {
		t.Fatalf("empty candidates err=%v want ErrNoPricingResolved", err)
	}
}

func TestResolveFailsClosedWhenEstimateBelowEveryThreshold(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	onlyLong := []PriceTier{{ProviderID: "p", ProviderModelID: "m", ContextTierName: "long_context", MinInputTokens: 200_000, InputPriceNanosPerMillion: 1, OutputPriceNanosPerMillion: 1, BillingMode: BillingOnline, EffectiveAt: now}}
	if _, err := Resolve(onlyLong, 100, BillingOnline); !errors.Is(err, ErrNoPricingResolved) {
		t.Fatalf("err=%v want ErrNoPricingResolved", err)
	}
}

func TestEstimateCostDeepseekFlashCacheHit(t *testing.T) {
	tier := PriceTier{
		ProviderID: "deepseek", ProviderModelID: "deepseek-v4-flash", ContextTierName: "default",
		InputPriceNanosPerMillion: 140_000_000, CachedInputPriceNanosPerMillion: nanos(2_800_000), OutputPriceNanosPerMillion: 280_000_000,
		EffectiveAt: time.Now(),
	}
	// 1,000,000 cache-hit input tokens should cost exactly $0.0028.
	cost, err := tier.EstimateCost(0, 1_000_000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 2_800_000 {
		t.Fatalf("cost=%d want=2800000 (%s)", cost, cost)
	}
	// A single token of fresh input should not round to zero: 1 * 140_000_000 / 1_000_000 = 140.
	cost, err = tier.EstimateCost(1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 140 {
		t.Fatalf("single-token cost=%d want=140", cost)
	}
}

func TestEstimateCostFailsClosedOnUnpricedDimension(t *testing.T) {
	tier := PriceTier{ProviderID: "gemini", ProviderModelID: "gemini-2.5-flash-lite", ContextTierName: "default", InputPriceNanosPerMillion: 100_000_000, OutputPriceNanosPerMillion: 400_000_000, EffectiveAt: time.Now()}
	if _, err := tier.EstimateCost(100, 50, 0, 10); !errors.Is(err, ErrNoPricingResolved) {
		t.Fatalf("cached-input without a price: err=%v want ErrNoPricingResolved", err)
	}
	if _, err := tier.EstimateCost(100, 0, 50, 10); !errors.Is(err, ErrNoPricingResolved) {
		t.Fatalf("cache-write without a price: err=%v want ErrNoPricingResolved", err)
	}
	// Zero counts against an unpriced dimension must not error.
	if _, err := tier.EstimateCost(100, 0, 0, 10); err != nil {
		t.Fatalf("zero cached/write tokens should not require a price: %v", err)
	}
}

func TestEstimateCostRejectsNegativeTokenCounts(t *testing.T) {
	tier := PriceTier{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default", InputPriceNanosPerMillion: 1, OutputPriceNanosPerMillion: 1, EffectiveAt: time.Now()}
	if _, err := tier.EstimateCost(-1, 0, 0, 0); !errors.Is(err, ErrInvalidPriceTier) {
		t.Fatalf("negative input tokens err=%v want ErrInvalidPriceTier", err)
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	now := time.Now()
	cases := []PriceTier{
		{ProviderModelID: "m", ContextTierName: "default", OutputPriceNanosPerMillion: 1, EffectiveAt: now},
		{ProviderID: "p", ContextTierName: "default", OutputPriceNanosPerMillion: 1, EffectiveAt: now},
		{ProviderID: "p", ProviderModelID: "m", OutputPriceNanosPerMillion: 1, EffectiveAt: now},
		{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default", MinInputTokens: -1, EffectiveAt: now},
		{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default", InputPriceNanosPerMillion: -1, EffectiveAt: now},
		{ProviderID: "p", ProviderModelID: "m", ContextTierName: "default"},
	}
	for i, tc := range cases {
		if err := tc.Validate(); !errors.Is(err, ErrInvalidPriceTier) {
			t.Fatalf("case %d: err=%v want ErrInvalidPriceTier", i, err)
		}
	}
}

func TestUSDNanosRoundTrip(t *testing.T) {
	got := USDFromDollars(8.66)
	if got.USD() < 8.659999 || got.USD() > 8.660001 {
		t.Fatalf("round trip=%v want ~8.66", got.USD())
	}
}
