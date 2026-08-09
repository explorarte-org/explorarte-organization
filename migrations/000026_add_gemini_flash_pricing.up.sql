-- model-routing.yaml routes research.worker to provider=gemini,
-- model=gemini-2.5-flash, but migration 000020 only seeded pricing for
-- gemini-2.5-flash-lite and other gemini variants, never the exact model
-- actually routed. Without this row, modelpricing.Resolve fails closed for
-- every real research.worker call. Real Gemini 2.5 Flash pricing (USD per
-- 1,000,000 tokens, stored as nanodollars = USD * 1e9), same source basis
-- as migration 000020.
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million, output_price_nanos_per_million, effective_at
) VALUES
    ('gemini', 'gemini-2.5-flash', 'default', 0, 300000000, NULL, NULL, 2500000000, NOW());
