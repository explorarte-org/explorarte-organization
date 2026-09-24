-- Migration 000082: seed pricing for deepseek/deepseek-flash, the model the owner moved
-- department.leader and department.worker to (docs/canonical/model-routing.yaml, same
-- change).
--
-- costgate.Reserve fails closed when modelpricing.Resolve finds no tier for a
-- provider/model (money must fail closed; see 000047), so a routed model with no price
-- row can never reserve cost and never dispatches. The routing change without this
-- migration would leave every department plan, worker and review unable to dispatch.
--
-- "deepseek-flash" is the API model id DeepSeek documents today
-- (https://api-docs.deepseek.com/quick_start/pricing; GET /models on the live API lists
-- exactly deepseek-flash and deepseek-v4-pro). It is served by DeepSeek-V4.1-Flash. The
-- legacy id deepseek-v4-flash (seeded in 000020) is still accepted by the API, routed to
-- the same model and billed at the Flash price, but the models behind it "have been
-- retired": routing uses the documented id, not the alias, and gets its own price row
-- because the published rate is not the one 000020 recorded for the alias.
--
-- DeepSeek publishes TWO prices by time of day (per 1M tokens):
--
--                          off-peak     peak
--     input, cache hit      $0.003      $0.006
--     input, cache miss     $0.15       $0.30
--     output                $0.60       $1.20
--
-- Peak hours are 01:00-04:00 and 06:00-10:00 UTC, Monday to Friday, excluding Chinese
-- public holidays; every other hour is off-peak. model_pricing has no time-of-day
-- dimension (a row is in force from its effective_at), so the PEAK price is stored. That
-- is the conservative choice for a fail-closed cost gate: a reservation can only ever be
-- larger than what DeepSeek bills, never smaller, and the committed cost of an off-peak
-- call is overstated by up to 2x. Modelling the schedule would be a schema change, not a
-- seed, and is deliberately not done here.
--
-- There is no long-context tier and cache writes are not priced separately (NULL).
-- deepseek-v4-flash's rows and deepseek-v4-pro's rows are NOT touched: rows are immutable
-- by design (000020). The provider wallet is per provider_id and deepseek already has one;
-- this migration creates none and moves no balance.
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
SELECT 'deepseek', 'deepseek-flash', 'default', 0, 300000000, 6000000, NULL, 1200000000, 'online', NOW()
WHERE NOT EXISTS (
    SELECT 1
    FROM model_pricing mp
    WHERE mp.provider_id = 'deepseek'
      AND mp.provider_model_id = 'deepseek-flash'
      AND mp.context_tier_name = 'default'
      AND mp.billing_mode = 'online'
);
