CREATE TABLE model_pricing (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) BETWEEN 1 AND 120),
    provider_model_id TEXT NOT NULL CHECK (length(trim(provider_model_id)) BETWEEN 1 AND 240),
    context_tier_name TEXT NOT NULL CHECK (length(trim(context_tier_name)) BETWEEN 1 AND 60),
    min_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (min_input_tokens >= 0),
    input_price_nanos_per_million BIGINT NOT NULL CHECK (input_price_nanos_per_million >= 0),
    cached_input_price_nanos_per_million BIGINT CHECK (cached_input_price_nanos_per_million IS NULL OR cached_input_price_nanos_per_million >= 0),
    cache_write_price_nanos_per_million BIGINT CHECK (cache_write_price_nanos_per_million IS NULL OR cache_write_price_nanos_per_million >= 0),
    output_price_nanos_per_million BIGINT NOT NULL CHECK (output_price_nanos_per_million >= 0),
    effective_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_pricing_unique_tier UNIQUE (provider_id, provider_model_id, context_tier_name, effective_at)
);

CREATE INDEX model_pricing_lookup_idx
    ON model_pricing (provider_id, provider_model_id, effective_at);

-- Price rows are an immutable, versioned rate card: a price change is a new
-- row with a later effective_at, never a mutation of a row a past call may
-- already have been priced against.
CREATE FUNCTION reject_model_pricing_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model pricing rows are immutable';
END;
$$;

CREATE TRIGGER model_pricing_no_mutation
BEFORE UPDATE OR DELETE ON model_pricing
FOR EACH ROW EXECUTE FUNCTION reject_model_pricing_mutation();

-- Real prices as of this migration (USD per 1,000,000 tokens, stored as
-- nanodollars = USD * 1e9). Google's context-caching storage fee and
-- grounding/search costs are per-time, not per-token, and are out of scope
-- for a per-call reservation ledger — deliberately not modeled here.
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million, output_price_nanos_per_million, effective_at
) VALUES
    ('deepseek', 'deepseek-v4-flash', 'default', 0, 140000000, 2800000, NULL, 280000000, NOW()),
    ('deepseek', 'deepseek-v4-pro', 'default', 0, 435000000, 3625000, NULL, 870000000, NOW()),
    ('openai_compatible', 'gpt-5.6-luna', 'default', 0, 200000000, 20000000, 250000000, 1200000000, NOW()),
    ('openai_compatible', 'gpt-5.6-luna', 'long_context', 272000, 400000000, 40000000, 500000000, 1800000000, NOW()),
    ('gemini', 'gemini-2.5-flash-lite', 'default', 0, 100000000, NULL, NULL, 400000000, NOW()),
    ('gemini', 'gemini-3.5-flash-lite', 'default', 0, 300000000, NULL, NULL, 2500000000, NOW()),
    ('gemini', 'gemini-3.5-flash', 'default', 0, 750000000, NULL, NULL, 4500000000, NOW()),
    ('gemini', 'gemini-3.6-flash', 'default', 0, 750000000, NULL, NULL, 3750000000, NOW()),
    ('gemini', 'gemini-2.5-pro', 'default', 0, 1250000000, NULL, NULL, 10000000000, NOW()),
    ('gemini', 'gemini-2.5-pro', 'long_context', 200000, 2500000000, NULL, NULL, 15000000000, NOW()),
    ('gemini', 'gemini-3.1-pro', 'default', 0, 2000000000, NULL, NULL, 12000000000, NOW()),
    ('gemini', 'gemini-3.1-pro', 'long_context', 200000, 4000000000, NULL, NULL, 18000000000, NOW());
