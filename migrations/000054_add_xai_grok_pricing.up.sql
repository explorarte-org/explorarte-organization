-- xAI pricing for grok-4.6, the model bound to research.adversarial_review
-- and therefore to investigacion/revisor_adversarial.
--
-- Without a row here modelpricing.Resolve fails closed and costgate refuses
-- every adversarial review, so this is a precondition of activation rather
-- than an optimization.
--
-- Rates are USD per 1,000,000 tokens, stored as nanodollars (USD * 1e9),
-- the same basis as migration 000020:
--
--   input   USD 2.00 / 1M  ->  2000000000
--   output  USD 6.00 / 1M  ->  6000000000
--
-- Source of decision: owner-supplied published xAI rates for the 500k
-- context configuration, recorded 2026-08-19. Not derived, not estimated.
--
-- One tier only, at min_input_tokens = 0. xAI documents that prompt-token
-- thresholds can carry different rates; if that applies to grok-4.6, a
-- SECOND row with the higher min_input_tokens and its own rates must be
-- added before any long-context review runs, or accounting will under-report
-- above the threshold. Seeding a second tier now would mean inventing a
-- price, which is worse than a documented gap.
--
-- No cached-input or cache-write rates are seeded: none were supplied, and
-- NULL means "not reported" rather than "free".
INSERT INTO model_pricing (
    provider_id, provider_model_id, context_tier_name, min_input_tokens,
    input_price_nanos_per_million, cached_input_price_nanos_per_million,
    cache_write_price_nanos_per_million, output_price_nanos_per_million,
    billing_mode, effective_at
) VALUES
    ('xai', 'grok-4.6', 'default', 0, 2000000000, NULL, NULL, 6000000000, 'online', NOW())
ON CONFLICT ON CONSTRAINT model_pricing_unique_tier DO NOTHING;
