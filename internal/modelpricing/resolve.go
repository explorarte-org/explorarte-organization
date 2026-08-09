package modelpricing

import (
	"context"
	"fmt"
	"time"
)

// Resolve picks the applicable tier from a set of candidates already
// filtered to one provider+model+point-in-time by the caller. It selects
// the tier with the largest MinInputTokens that does not exceed
// estimatedInputTokens — i.e. the most specific context-length band that
// still applies. A candidate set with no tier at all, or none whose
// threshold the estimate clears, fails closed.
//
// Price rows are an immutable, append-only rate card (see migration
// 000020): a price change for the same context band is a new row with a
// later EffectiveAt, never a mutation of the old one. The caller's
// point-in-time filter can therefore legitimately return several rows for
// the same MinInputTokens band (its full price history up to that point),
// and MinInputTokens alone cannot break that tie deterministically. Ties
// are broken by the latest EffectiveAt — the currently valid version of
// that band — so which row wins never depends on undefined SQL row order.
func Resolve(candidates []PriceTier, estimatedInputTokens int64) (PriceTier, error) {
	if estimatedInputTokens < 0 {
		return PriceTier{}, fmt.Errorf("%w: estimated input tokens must be non-negative", ErrInvalidPriceTier)
	}
	var best PriceTier
	found := false
	for _, candidate := range candidates {
		if candidate.MinInputTokens > estimatedInputTokens {
			continue
		}
		if !found ||
			candidate.MinInputTokens > best.MinInputTokens ||
			(candidate.MinInputTokens == best.MinInputTokens && candidate.EffectiveAt.After(best.EffectiveAt)) {
			best = candidate
			found = true
		}
	}
	if !found {
		return PriceTier{}, fmt.Errorf("%w: no tier at or below %d input tokens", ErrNoPricingResolved, estimatedInputTokens)
	}
	return best, nil
}

// Service resolves prices through a Store, applying Resolve's tier-selection
// rule to whatever the store returns for a provider+model at a point in time.
type Service struct{ store Store }

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidPriceTier)
	}
	return &Service{store: store}, nil
}

func (s *Service) Resolve(ctx context.Context, providerID, providerModelID string, estimatedInputTokens int64, asOf time.Time) (PriceTier, error) {
	tiers, err := s.store.ListTiers(ctx, providerID, providerModelID, asOf)
	if err != nil {
		return PriceTier{}, err
	}
	return Resolve(tiers, estimatedInputTokens)
}

func (s *Service) Upsert(ctx context.Context, tier PriceTier) (PriceTier, error) {
	if err := tier.Validate(); err != nil {
		return PriceTier{}, err
	}
	return s.store.Upsert(ctx, tier)
}
