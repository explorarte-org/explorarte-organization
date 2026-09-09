-- Migration 000069: canonical Standard pricing for the Research pool's
-- Mistral model, verified 2026-09-09 against:
--   1. GET https://api.mistral.ai/v1/models with a real account credential
--      -- ministral-8b-2512 exists, deprecation=null, capabilities.
--      completion_chat=true. ministral-8b-latest is a live alias of the
--      SAME model (aliases: ["ministral-8b-latest"]), not a distinct id.
--   2. https://docs.mistral.ai/inference/pricing/ (official Standard price
--      page) -- Ministral 3 8B: USD 0.15 / 1M input, USD 0.15 / 1M output.
--
-- Pinned to the versioned id ministral-8b-2512, not the mutable alias: an
-- alias can be repointed by the provider at any time without notice, which
-- would silently drift dispatch pricing away from what CostGate reserves
-- against. The alias remains usable for RESEARCH_MISTRAL_MODEL config
-- convenience but the priced, canonical identity is the versioned id.
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
    'mistral', 'ministral-8b-2512', 'standard', 0,
    150000000, 150000000,
    'online', '2026-09-09T00:00:00Z'::timestamptz
)
ON CONFLICT (provider_id, provider_model_id, context_tier_name, billing_mode, effective_at) DO NOTHING;
