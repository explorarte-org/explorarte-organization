-- billing_mode is a real pricing dimension, not something ContextTierName can
-- stand in for: a provider can charge different rates for the same
-- provider_id+provider_model_id+context_tier_name depending on whether the
-- call is synchronous ("online") or a discounted asynchronous Batch API job
-- ("batch"). Without this column, two rows sharing context_tier_name='default'
-- and min_input_tokens=0 (one online, one batch) would be indistinguishable to
-- Resolve()'s MinInputTokens/EffectiveAt tie-break.
--
-- ADD COLUMN ... DEFAULT is a metadata-only change in PostgreSQL 11+ (no
-- table rewrite, no per-row UPDATE), so it does not fire
-- reject_model_pricing_mutation — existing immutable rows are untouched and
-- become 'online' explicitly, which is correct: every row inserted before
-- this migration priced a chat dispatch call, and every chat dispatch call
-- is synchronous.
ALTER TABLE model_pricing ADD COLUMN billing_mode TEXT NOT NULL DEFAULT 'online' CHECK (billing_mode IN ('online', 'batch'));

ALTER TABLE model_pricing DROP CONSTRAINT model_pricing_unique_tier;
ALTER TABLE model_pricing ADD CONSTRAINT model_pricing_unique_tier UNIQUE (provider_id, provider_model_id, context_tier_name, billing_mode, effective_at);

-- Gemini Embedding pricing (R29): input-only (no output tokens for an
-- embedding call), online and batch as two distinct, independently priced
-- rows for the same provider+model. Real prices provided by the user, not
-- placeholders: $0.20/1M tokens online, $0.10/1M tokens batch.
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million, output_price_nanos_per_million, billing_mode, effective_at
) VALUES
    ('gemini', 'gemini-embedding-2', 'default', 0, 200000000, NULL, NULL, 0, 'online', NOW()),
    ('gemini', 'gemini-embedding-2', 'default', 0, 100000000, NULL, NULL, 0, 'batch', NOW());
