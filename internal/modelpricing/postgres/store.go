package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("modelpricing store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

var _ modelpricing.Store = (*Store)(nil)

func (s *Store) ListTiers(ctx context.Context, providerID, providerModelID string, billingMode modelpricing.BillingMode, asOf time.Time) ([]modelpricing.PriceTier, error) {
	rows, err := s.pool.Query(ctx, `
SELECT provider_id, provider_model_id, context_tier_name, min_input_tokens,
       input_price_nanos_per_million, cached_input_price_nanos_per_million,
       cache_write_price_nanos_per_million, output_price_nanos_per_million,
       billing_mode, effective_at, created_at
FROM model_pricing
WHERE provider_id=$1 AND provider_model_id=$2 AND billing_mode=$3 AND effective_at <= $4`,
		providerID, providerModelID, string(billingMode), asOf.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tiers := make([]modelpricing.PriceTier, 0)
	for rows.Next() {
		var (
			tier                              modelpricing.PriceTier
			cachedInputNanos, cacheWriteNanos *int64
			billingModeValue                  string
		)
		if err := rows.Scan(
			&tier.ProviderID, &tier.ProviderModelID, &tier.ContextTierName, &tier.MinInputTokens,
			&tier.InputPriceNanosPerMillion, &cachedInputNanos, &cacheWriteNanos, &tier.OutputPriceNanosPerMillion,
			&billingModeValue, &tier.EffectiveAt, &tier.CreatedAt,
		); err != nil {
			return nil, err
		}
		tier.BillingMode = modelpricing.BillingMode(billingModeValue)
		tier.EffectiveAt = tier.EffectiveAt.UTC()
		tier.CreatedAt = tier.CreatedAt.UTC()
		if cachedInputNanos != nil {
			value := modelpricing.USDNanos(*cachedInputNanos)
			tier.CachedInputPriceNanosPerMillion = &value
		}
		if cacheWriteNanos != nil {
			value := modelpricing.USDNanos(*cacheWriteNanos)
			tier.CacheWritePriceNanosPerMillion = &value
		}
		tiers = append(tiers, tier)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tiers, nil
}

func (s *Store) Upsert(ctx context.Context, tier modelpricing.PriceTier) (modelpricing.PriceTier, error) {
	if tier.EffectiveAt.IsZero() {
		tier.EffectiveAt = time.Now().UTC()
	}
	var cachedInputNanos, cacheWriteNanos *int64
	if tier.CachedInputPriceNanosPerMillion != nil {
		value := int64(*tier.CachedInputPriceNanosPerMillion)
		cachedInputNanos = &value
	}
	if tier.CacheWritePriceNanosPerMillion != nil {
		value := int64(*tier.CacheWritePriceNanosPerMillion)
		cacheWriteNanos = &value
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million, output_price_nanos_per_million, billing_mode, effective_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (provider_id, provider_model_id, context_tier_name, billing_mode, effective_at) DO NOTHING
RETURNING created_at`,
		tier.ProviderID, tier.ProviderModelID, tier.ContextTierName, tier.MinInputTokens,
		int64(tier.InputPriceNanosPerMillion), cachedInputNanos, cacheWriteNanos, int64(tier.OutputPriceNanosPerMillion), string(tier.BillingMode), tier.EffectiveAt.UTC())
	if err := row.Scan(&tier.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return modelpricing.PriceTier{}, modelpricing.ErrConflict
		}
		return modelpricing.PriceTier{}, err
	}
	tier.CreatedAt = tier.CreatedAt.UTC()
	return tier, nil
}
