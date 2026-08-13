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
INSERT INTO model_pricing (provider_id, provider_model_id, context_tier_name, min_input_tokens, input_price_nanos_per_million, cached_input_price_nanos_per_million, cache_write_price_nanos_per_million, output_price_nanos_per_million, effective_at) VALUES
    ('openai_responses', 'gpt-5.6-luna', 'default', 0, 200000000, 20000000, 250000000, 1200000000, NOW()),
    ('openai_responses', 'gpt-5.6-luna', 'long_context', 272000, 400000000, 40000000, 500000000, 1800000000, NOW());

-- Wallet balance: mirrors openai_compatible's seeded balance for the same
-- reason as the pricing above. This is a placeholder starting balance, not
-- a considered budget decision -- whoever owns provider spend limits
-- should revisit it before this routing carries real CEO traffic.
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at) VALUES
    ('openai_responses', 9700000000, 0, NOW());
