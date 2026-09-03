-- Migration 000047: seed pricing and a wallet for the openai_responses
-- provider, which docs/canonical/model-routing.yaml has routed
-- executive.ceo to since before this migration existed.
--
-- ORG-AUDIT-003: model_pricing (000020) and provider_wallets (000021)
-- seeded deepseek, openai_compatible, gemini, and (000039) mimo as a
-- subscription. openai_responses -- the provider executive.ceo's
-- canonical routing names -- had no row in either table. costgate.Reserve
-- (internal/modelruntime/costgate/gate.go) fails closed when
-- modelpricing.Resolve finds no tier and again when the wallet lookup
-- misses, by design (money must fail closed) -- so this was never a
-- silent overspend risk, it was the CEO's routed model.invoke calls
-- having no path to ever reserve cost and dispatch.
--
-- Pricing mirrors openai_compatible/gpt-5.6-luna's existing rate card
-- exactly: it is the same underlying model, and openai_responses is a
-- different transport (OpenAI's Responses API surface) for the same
-- provider account, not a different pricing agreement. Copying an
-- already-approved rate card is the only choice here that does not invent
-- a number.
--
-- CUTOVER-REHEARSAL-001: production already carries a manually-seeded
-- openai_responses wallet + both pricing tiers (2026-08-10, predating this
-- migration -- see HANDOFF-2026-08-10-noche.md), so a plain unconditional
-- INSERT is wrong twice over:
--
--   * model_pricing's unique constraint is scoped by effective_at, and this
--     statement uses NOW() -- an unconditional INSERT would NOT collide, it
--     would silently create a second, semantically-duplicate row for the
--     same tier with a later effective_at. Resolve() picks the
--     latest-effective row, so this would flip production onto a
--     freshly-seeded row that happens to carry the same numbers today but
--     is no longer the row any past call was actually priced against --
--     defeating the entire point of the immutable versioned rate card.
--     The guard below is therefore keyed on tier identity
--     (provider_id, provider_model_id, context_tier_name, billing_mode),
--     deliberately excluding effective_at, so an already-present tier
--     (by whatever effective_at it was originally seeded with) is left
--     alone rather than shadowed by a newer duplicate.
--   * provider_wallets has no such versioning; a real, possibly-nonzero
--     balance already exists in production under this provider_id, and
--     the fix must not touch it.
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
      ('openai_responses', 'gpt-5.6-luna', 'default',
       0, 200000000, 20000000, 250000000, 1200000000, 'online'),

      ('openai_responses', 'gpt-5.6-luna', 'long_context',
       272000, 400000000, 40000000, 500000000, 1800000000, 'online')
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

-- Wallet balance: mirrors openai_compatible's seeded balance for the same
-- reason as the pricing above. This is a placeholder starting balance, not
-- a considered budget decision -- whoever owns provider spend limits
-- should revisit it before this routing carries real CEO traffic.
--
-- ON CONFLICT DO NOTHING: if a wallet for this provider_id already exists
-- (e.g. production's pre-existing manual seed), its balance/reserved
-- amount is real state that must not be overwritten by a placeholder.
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at) VALUES
    ('openai_responses', 9700000000, 0, NOW())
ON CONFLICT (provider_id) DO NOTHING;
