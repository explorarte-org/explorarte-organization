package modelpricing

import (
	"context"
	"time"
)

// Store is the persistence boundary for price tiers. ListTiers returns every
// tier for a provider+model whose EffectiveAt is at or before asOf — the
// caller (Resolve) picks the applicable one; the store never filters by
// MinInputTokens itself, so tiering logic stays in one place.
type Store interface {
	ListTiers(ctx context.Context, providerID, providerModelID string, asOf time.Time) ([]PriceTier, error)
	Upsert(ctx context.Context, tier PriceTier) (PriceTier, error)
}
