package modelpricing

import (
	"context"
	"time"
)

// Store is the persistence boundary for price tiers. ListTiers returns every
// tier for a provider+model+billingMode whose EffectiveAt is at or before
// asOf — the caller (Resolve) picks the applicable one; the store never
// filters by MinInputTokens itself, so tiering logic stays in one place.
// billingMode is filtered here (an exact-match dimension, like provider_id
// itself) rather than left to Resolve alone — Resolve still re-checks it
// defensively so the pure function stays correct even when called directly
// with a hand-built candidate slice mixing billing modes (as tests do).
type Store interface {
	ListTiers(ctx context.Context, providerID, providerModelID string, billingMode BillingMode, asOf time.Time) ([]PriceTier, error)
	Upsert(ctx context.Context, tier PriceTier) (PriceTier, error)
}
