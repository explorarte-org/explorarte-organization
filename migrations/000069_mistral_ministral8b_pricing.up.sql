-- Migration 000069: canonical Standard pricing for the Research pool's
-- Mistral model, verified from Mistral's official Standard price page
-- (verified 2026-09-08): USD 0.15 per 1M input tokens and USD 0.15 per 1M
-- output tokens for ministral-8b-latest (online billing).
--
-- Unlike migration 000068 (Cloudflare), this row carries REAL pricing:
-- Mistral is NOT a subscription provider. Every dispatch resolves this
-- tier, reserves estimated cost against the provider wallet, and
-- reconciles actual usage -- the local credit-ceiling barrier when the
-- deployment seeds the mistral wallet with the owner-configured
-- MISTRAL_CREDIT_CEILING_USD (idempotent provisioning, never auto-raised).
--
-- Units: USDNanos (1 USD = 1e9 nanos). $0.15 / 1M tokens
-- = 0.15 * 1e9 = 150000000 nanos per 1M tokens.
--
-- effective_at is FIXED (not NOW()) so the pricing identity is
-- reproducible and the ON CONFLICT guard is real across re-runs.
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, output_price_nanos_per_million,
    billing_mode, effective_at
) VALUES (
    'mistral', 'ministral-8b-latest', 'standard', 0,
    150000000, 150000000,
    'online', '2026-09-08T00:00:00Z'::timestamptz
)
ON CONFLICT (provider_id, provider_model_id, context_tier_name, billing_mode, effective_at) DO NOTHING;
