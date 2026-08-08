package modelpricing

import (
	"errors"
	"testing"
	"time"
)

func nanos(n int64) *USDNanos { v := USDNanos(n); return &v }

func TestResolvePicksMostSpecificContextTierBelowThreshold(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	short := PriceTier{ProviderID: "openai_compatible", ProviderModelID: "gpt-5.6-luna", ContextTierName: "default", MinInputTokens: 0, InputPriceNanosPerMillion: 200_000_000, OutputPriceNanosPerMillion: 1_200_000_000, EffectiveAt: now}
	long := PriceTier{ProviderID: "openai_compatible", ProviderModelID: "gpt-5.6-luna", ContextTierName: "long_context", MinInputTokens: 272_000, InputPriceNanosPerMillion: 400_000_000, OutputPriceNanosPerMillion: 1_800_000_000, EffectiveAt: now}
	candidates := []PriceTier{short, long}

	got, err := Resolve(candidates, 1_000)
	if err != nil || got.ContextTierName != "default" {
		t.Fatalf("below threshold: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 271_999)
	if err != nil || got.ContextTierName != "default" {
		t.Fatalf("just below threshold: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 272_000)
	if err != nil || got.ContextTierName != "long_context" {
		t.Fatalf("at threshold: got=%+v err=%v", got, err)
	}
	got, err = Resolve(candidates, 500_000)
	if err != nil || got.ContextTierName != "long_context" {
		t.Fatalf("above threshold: got=%+v err=%v", got, err)
	}
}

func TestResolveFailsClosedWithoutAnyTier(t *testing.T) {
	if _, err := Resolve(nil, 100); !errors.Is(err, ErrNoPricingResolved) {
		t.Fatalf("empty candidates err=%v want ErrNoPricingResolved", err)
	}
}

func TestResolveFailsClosedWhenEstimateBelowEveryThreshold(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	onlyLong := []PriceTier{{ProviderID: "p", ProviderModelID: "m", ContextTierName: "long_context", MinInputTokens: 200_000, InputPriceNanosPerMillion: 1, OutputPriceNanosPerMillion: 1, EffectiveAt: now}}
	if _, err := Resolve(onlyLong, 100); !errors.Is(err, ErrNoPricingResolved) {
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
