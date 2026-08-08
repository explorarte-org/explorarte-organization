CREATE TABLE provider_wallets (
    provider_id TEXT PRIMARY KEY CHECK (length(trim(provider_id)) BETWEEN 1 AND 120),
    balance_usd_nanos BIGINT NOT NULL CHECK (balance_usd_nanos >= 0),
    reserved_usd_nanos BIGINT NOT NULL DEFAULT 0 CHECK (reserved_usd_nanos >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (reserved_usd_nanos <= balance_usd_nanos)
);

CREATE TABLE provider_wallet_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES provider_wallets(provider_id) ON DELETE RESTRICT,
    invocation_id BIGINT NOT NULL REFERENCES model_invocations(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('reserved','committed','released')),
    amount_usd_nanos BIGINT NOT NULL CHECK (amount_usd_nanos >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT provider_wallet_events_unique_kind UNIQUE (provider_id, invocation_id, kind)
);

CREATE INDEX provider_wallet_events_invocation_idx
    ON provider_wallet_events (provider_id, invocation_id);

-- Wallet events are an append-only ledger: a reservation's lifecycle is
-- reconstructed by reading its reserved/committed/released rows, never by
-- mutating one in place.
CREATE FUNCTION reject_provider_wallet_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'provider wallet events are immutable';
END;
$$;

CREATE TRIGGER provider_wallet_events_no_mutation
BEFORE UPDATE OR DELETE ON provider_wallet_events
FOR EACH ROW EXECUTE FUNCTION reject_provider_wallet_event_mutation();

-- Real starting balances as reported for this ledger. alibaba_token_plan_via_claude_code
-- is a flat token plan with no marginal per-call cost and deliberately has
-- no wallet — a call against it is never priced or reserved.
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at) VALUES
    ('deepseek', 8660000000, 0, NOW()),
    ('gemini', 10000000000, 0, NOW()),
    ('openai_compatible', 9700000000, 0, NOW());
