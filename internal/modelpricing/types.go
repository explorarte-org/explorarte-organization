package modelpricing

import (
	"fmt"
	"strings"
	"time"
)

// USDNanos is a USD amount as an int64 count of 1e-9 USD ("nanodollars").
// $1.00 == 1_000_000_000. Money is never represented as a float in this
// package: token-count * price arithmetic stays exact in integers, and the
// smallest real per-call costs (e.g. deepseek-v4-flash cache-hit input at
// $0.0028 per million tokens) are still representable without rounding to
// zero.
type USDNanos int64

const usdNanosPerDollar = 1_000_000_000

// BillingMode distinguishes a provider call charged at its synchronous,
// interactive rate ("online") from one submitted as an asynchronous,
// discounted Batch API job ("batch"). It is a real pricing dimension, not
// something ContextTierName can stand in for: two rows sharing the same
// ContextTierName and MinInputTokens (e.g. both "default"/0) but different
// BillingMode price the same provider+model at two different rates for the
// same context band, and Resolve must never conflate them.
type BillingMode string

const (
	BillingOnline BillingMode = "online"
	BillingBatch  BillingMode = "batch"
)

func (m BillingMode) Valid() bool {
	switch m {
	case BillingOnline, BillingBatch:
		return true
	default:
		return false
	}
}

// USD returns the amount as a float64 dollar value, for display only —
// never for further money arithmetic.
func (n USDNanos) USD() float64 { return float64(n) / usdNanosPerDollar }

// USDFromDollars converts a float64 dollar amount (e.g. from a CLI flag)
// into USDNanos. Precision beyond 1e-9 USD is not representable and is
// truncated, which is acceptable at this scale for real prices/balances.
func USDFromDollars(usd float64) USDNanos {
	return USDNanos(usd * usdNanosPerDollar)
}

func (n USDNanos) String() string { return fmt.Sprintf("$%.9f", n.USD()) }

// PriceTier is one versioned, immutable price row for a provider+model,
// optionally scoped to a context-length threshold (e.g. gpt-5.6-luna and
// gemini's *-pro models charge more once a call's input tokens cross a
// threshold). All price fields are USD per one million tokens, expressed
// in nanodollars.
type PriceTier struct {
	ProviderID                      string
	ProviderModelID                 string
	ContextTierName                 string
	MinInputTokens                  int64
	InputPriceNanosPerMillion       USDNanos
	CachedInputPriceNanosPerMillion *USDNanos
	CacheWritePriceNanosPerMillion  *USDNanos
	OutputPriceNanosPerMillion      USDNanos
	BillingMode                     BillingMode
	EffectiveAt                     time.Time
	CreatedAt                       time.Time
}

func (t PriceTier) Validate() error {
	if strings.TrimSpace(t.ProviderID) == "" || strings.TrimSpace(t.ProviderModelID) == "" {
		return fmt.Errorf("%w: provider and model are required", ErrInvalidPriceTier)
	}
	if strings.TrimSpace(t.ContextTierName) == "" {
		return fmt.Errorf("%w: context tier name is required", ErrInvalidPriceTier)
	}
	if !t.BillingMode.Valid() {
		return fmt.Errorf("%w: invalid billing mode %q", ErrInvalidPriceTier, t.BillingMode)
	}
	if t.MinInputTokens < 0 {
		return fmt.Errorf("%w: min input tokens must be non-negative", ErrInvalidPriceTier)
	}
	if t.InputPriceNanosPerMillion < 0 || t.OutputPriceNanosPerMillion < 0 {
		return fmt.Errorf("%w: input/output prices must be non-negative", ErrInvalidPriceTier)
	}
	if t.CachedInputPriceNanosPerMillion != nil && *t.CachedInputPriceNanosPerMillion < 0 {
		return fmt.Errorf("%w: cached input price must be non-negative", ErrInvalidPriceTier)
	}
	if t.CacheWritePriceNanosPerMillion != nil && *t.CacheWritePriceNanosPerMillion < 0 {
		return fmt.Errorf("%w: cache write price must be non-negative", ErrInvalidPriceTier)
	}
	if t.EffectiveAt.IsZero() {
		return fmt.Errorf("%w: effective_at is required", ErrInvalidPriceTier)
	}
	return nil
}

// EstimateCost computes the USD cost of a call's token usage against this
// tier. cachedInputTokens/cacheWriteTokens must be zero unless the tier
// prices that dimension — a non-zero count against an unpriced dimension
// fails closed rather than silently charging $0 or falling back to the
// plain input price.
func (t PriceTier) EstimateCost(inputTokens, cachedInputTokens, cacheWriteTokens, outputTokens int64) (USDNanos, error) {
	if inputTokens < 0 || cachedInputTokens < 0 || cacheWriteTokens < 0 || outputTokens < 0 {
		return 0, fmt.Errorf("%w: token counts must be non-negative", ErrInvalidPriceTier)
	}
	total, err := scaleNanos(inputTokens, t.InputPriceNanosPerMillion)
	if err != nil {
		return 0, err
	}
	outputCost, err := scaleNanos(outputTokens, t.OutputPriceNanosPerMillion)
	if err != nil {
		return 0, err
	}
	total += outputCost

	if cachedInputTokens > 0 {
		if t.CachedInputPriceNanosPerMillion == nil {
			return 0, fmt.Errorf("%w: %s/%s (%s) has no cached-input price configured", ErrNoPricingResolved, t.ProviderID, t.ProviderModelID, t.ContextTierName)
		}
		cost, err := scaleNanos(cachedInputTokens, *t.CachedInputPriceNanosPerMillion)
		if err != nil {
			return 0, err
		}
		total += cost
	}
	if cacheWriteTokens > 0 {
		if t.CacheWritePriceNanosPerMillion == nil {
			return 0, fmt.Errorf("%w: %s/%s (%s) has no cache-write price configured", ErrNoPricingResolved, t.ProviderID, t.ProviderModelID, t.ContextTierName)
		}
		cost, err := scaleNanos(cacheWriteTokens, *t.CacheWritePriceNanosPerMillion)
		if err != nil {
			return 0, err
		}
		total += cost
	}
	return total, nil
}

func scaleNanos(tokens int64, pricePerMillion USDNanos) (USDNanos, error) {
	if tokens == 0 || pricePerMillion == 0 {
		return 0, nil
	}
	product := tokens * int64(pricePerMillion)
	if product/tokens != int64(pricePerMillion) {
		return 0, fmt.Errorf("%w: cost computation overflowed", ErrInvalidPriceTier)
	}
	return USDNanos(product / 1_000_000), nil
}
