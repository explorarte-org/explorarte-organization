-- Migration 000069: canonical Standard pricing for the Research pool's
-- Mistral model, verified from Mistral's official Standard price page
-- (verified 2026-09-08): USD 0.15 per 1M input tokens and USD 0.15 per 1M
-- output tokens for ministral-8b-latest (online (BillingOnline) billing).
--
-- Unlike migration 000068 (Cloudflare), this row carries REAL pricing:
-- Mistral is NOT a subscription provider. Every dispatch resolves this
-- tier, reserves estimated cost against the provider wallet, and
-- reconciles actual usage -- the local credit-ceiling barrier when the
-- deployment seeds the mistral wallet with the owner-configured
-- MISTRAL_CREDIT_CEILING_USD.
--
-- Units: USDNanos (1 USD = 1e9). 0.15 USD / 1M tokens = 150000 nanos/1M.
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, output_price_nanos_per_million,
    billing_mode, effective_at
) VALUES (
    'mistral', 'ministral-8b-latest', 'standard', 0,
    150000, 150000,
    'online', NOW()
)
ON CONFLICT (provider_id, provider_model_id, context_tier_name, billing_mode, effective_at) DO NOTHING;
