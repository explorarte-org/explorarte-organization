-- Migration 000085: seed pricing for openai_responses/gpt-6-luna, the model the owner
-- moved executive.ceo to on 2026-09-28, at reasoning_effort max
-- (docs/canonical/model-routing.yaml, same change).
--
-- costgate.Reserve fails closed when modelpricing.Resolve finds no tier for a
-- provider/model (see 000047), so the routing change without this migration would
-- leave the CEO unable to call its own model.
--
-- The numbers are the provider's published rate card for GPT-6 Luna, as the owner
-- supplied it from the provider's model page (text tokens, per 1M tokens):
--
--     input         $0.10
--     cached input  $0.01     (10% of the uncached input rate)
--     cache writes  $0.125    (1.25x the uncached input rate)
--     output        $0.50
--
-- and the same long-prompt rule as GPT-6 Sol (000083): a request with more than 272K
-- input tokens is priced at 2x the input and cache rates and 1.5x output for the FULL
-- request, stored as a second tier:
--
--     long_context (>= 272000 input tokens)
--         input $0.20, cached input $0.02, cache writes $0.25, output $0.75
--
-- Not modelled, deliberately: regional processing (+10% where available), Batch/Flex
-- (50%) and Fast mode (2x). None is selected by this deployment's requests, which are
-- Standard.
--
-- gpt-6-sol's rows are NOT touched: rows are immutable by design (000020). The provider
-- wallet is per provider_id and openai_responses already has one (000047).
--
-- Guarded on tier identity, not effective_at, exactly as 000047 and 000083 are.
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
      ('openai_responses', 'gpt-6-luna', 'default',
       0, 100000000, 10000000, 125000000, 500000000, 'online'),

      ('openai_responses', 'gpt-6-luna', 'long_context',
       272000, 200000000, 20000000, 250000000, 750000000, 'online')
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
