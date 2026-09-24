-- Migration 000082: seed pricing for gemini/gemini-3.8-flash, the model the owner
-- moved department.leader and department.worker to (docs/canonical/model-routing.yaml,
-- same change).
--
-- costgate.Reserve fails closed when modelpricing.Resolve finds no tier for a
-- provider/model (money must fail closed; see 000047), so a routed model with no price
-- row can never reserve cost and never dispatches. The routing change without this
-- migration would leave every department plan, worker and review unable to dispatch.
--
-- The numbers are Google's published Gemini API rate card for gemini-3.8-flash
-- (https://ai.google.dev/gemini-api/docs/pricing, paid tier, Standard, per 1M tokens),
-- which states TWO prices for the same model:
--
--                        through 2026-12-31    from 2027-01-01
--     input                    $0.75               $1.50
--     output (with thinking)   $3.75               $7.50
--     context caching          $0.075              $0.15
--
-- There is no long-context tier on that page, so there is one tier, "default".
-- Context-cache STORAGE ($0.50 -> $1.00 per 1M tokens per hour) is per-time, not
-- per-token, and is deliberately not modelled, exactly as 000020 documents for the
-- other Gemini models. Cache writes are not priced separately by Google (NULL).
--
-- A price change is a NEW row with a later effective_at, never a mutation (000020), and
-- Resolve() reads the row in force at the requested time. So both prices are seeded now:
-- the current one effective at migration time, and the 2027 one effective at
-- 2027-01-01T00:00:00Z. Nothing has to be remembered on January 1st -- and a reservation
-- made after that date can never be priced at the promotional rate. (The page does not
-- state the timezone of the changeover; UTC midnight is the earliest reading that could
-- apply, and the difference is hours.)
--
-- gemini-3.5-flash-lite's rows are NOT touched: rows are immutable by design, and a price
-- a past call was already billed against must never change. The provider wallet is per
-- provider_id and gemini already has one; this migration creates none and moves no balance.
--
-- Guarded on tier identity, exactly as 000047 is: an already-present tier is left alone
-- rather than shadowed by a newer duplicate that Resolve() would silently prefer.
INSERT INTO model_pricing (
    provider_id,
    provider_model_id,
    context_tier_name,
    min_input_tokens,
    input_price_nanos_per_million,
    cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million,
    output_price_nanos_per_million,
    billing_mode,
    effective_at
)
SELECT 'gemini', 'gemini-3.8-flash', 'default', 0, 750000000, 75000000, NULL, 3750000000, 'online', NOW()
WHERE NOT EXISTS (
    SELECT 1
    FROM model_pricing mp
    WHERE mp.provider_id = 'gemini'
      AND mp.provider_model_id = 'gemini-3.8-flash'
      AND mp.context_tier_name = 'default'
      AND mp.billing_mode = 'online'
);

INSERT INTO model_pricing (
    provider_id,
    provider_model_id,
    context_tier_name,
    min_input_tokens,
    input_price_nanos_per_million,
    cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million,
    output_price_nanos_per_million,
    billing_mode,
    effective_at
)
SELECT 'gemini', 'gemini-3.8-flash', 'default', 0, 1500000000, 150000000, NULL, 7500000000, 'online', TIMESTAMPTZ '2027-01-01 00:00:00+00'
WHERE NOT EXISTS (
    SELECT 1
    FROM model_pricing mp
    WHERE mp.provider_id = 'gemini'
      AND mp.provider_model_id = 'gemini-3.8-flash'
      AND mp.context_tier_name = 'default'
      AND mp.billing_mode = 'online'
      AND mp.effective_at = TIMESTAMPTZ '2027-01-01 00:00:00+00'
);
