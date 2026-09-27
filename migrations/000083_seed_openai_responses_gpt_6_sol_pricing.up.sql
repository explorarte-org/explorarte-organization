-- Migration 000083: seed pricing for openai_responses/gpt-6-sol, the model the owner
-- moved executive.ceo to (docs/canonical/model-routing.yaml, same change).
--
-- costgate.Reserve fails closed when modelpricing.Resolve finds no tier for a
-- provider/model (money must fail closed; see 000047), so a routed model with no
-- price row can never reserve cost and never dispatches. The routing change without
-- this migration would leave the CEO unable to call its own model.
--
-- The numbers are the provider's published rate card for GPT-6 Sol, as the owner
-- supplied it from the provider's model page (text tokens, per 1M tokens):
--
--     input         $2.00
--     cached input  $0.20     (10% of the uncached input rate)
--     cache writes  $2.50     (1.25x the uncached input rate)
--     output        $10.00
--
-- and the provider's long-prompt rule, from the same page: a request with more than 272K
-- input tokens is priced at 2x the input and cache rates and 1.5x output for the FULL
-- request. That is the same threshold and multipliers gpt-5.6-luna's tiers encode
-- (000020/000047), so it is stored the same way, as a second tier:
--
--     long_context (>= 272000 input tokens)
--         input $4.00, cached input $0.40, cache writes $5.00, output $15.00
--
-- Not modelled, deliberately: regional processing (+10% where available), Batch/Flex
-- (50%) and Fast mode (2x). None is selected by this deployment's requests, which are
-- Standard.
--
-- gpt-5.6-luna's rows are NOT touched: rows are immutable by design (000020), and
-- executive.observer still routes to that model. The provider wallet is per provider_id
-- and openai_responses already has one (000047); this migration creates none.
--
-- Guarded on tier identity, not effective_at, exactly as 000047 is.
WITH seeds (
    provider_id,
    provider_model_id,
    context_tier_name,
    min_input_tokens,
    input_price_nanos_per_million,
    cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million,
    output_price_nanos_per_million,
    billing_mode
) AS (
    VALUES
      ('openai_responses', 'gpt-6-sol', 'default',
       0, 2000000000, 200000000, 2500000000, 10000000000, 'online'),

      ('openai_responses', 'gpt-6-sol', 'long_context',
       272000, 4000000000, 400000000, 5000000000, 15000000000, 'online')
)
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
SELECT
    s.provider_id,
    s.provider_model_id,
    s.context_tier_name,
    s.min_input_tokens,
    s.input_price_nanos_per_million,
    s.cached_input_price_nanos_per_million,
    s.cache_write_price_nanos_per_million,
    s.output_price_nanos_per_million,
    s.billing_mode,
    NOW()
FROM seeds s
WHERE NOT EXISTS (
    SELECT 1
    FROM model_pricing mp
    WHERE mp.provider_id = s.provider_id
      AND mp.provider_model_id = s.provider_model_id
      AND mp.context_tier_name = s.context_tier_name
      AND mp.billing_mode = s.billing_mode
);
