-- R29 phase 6: a second, independent invocation path for the same
-- per-provider wallet. An embedding call (rag.Query/Search, memory.Search,
-- Reindex's batch submission) is not a chat dispatch — there is no owning
-- task_attempt, context_snapshot, or model_profile the way model_invocations
-- requires — so it gets its own lightweight identity table instead of being
-- forced through model_invocations just to satisfy provider_wallet_events'
-- foreign key. Both paths still debit the SAME provider_wallets row: an
-- embedding call and a chat call against the same provider draw from one
-- real dollar balance.
--
-- Unlike model_invocations, embedding_invocations carries no dispatch/claim
-- state machine (no task_attempt, no lease) — success/failure of the spend
-- itself is recorded entirely by which wallet event kind lands
-- (reserved-only vs reserved+committed vs reserved+released), same as the
-- chat path already does.
CREATE TABLE embedding_invocations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    actor_role_id TEXT NOT NULL,
    task_id BIGINT REFERENCES tasks(id) ON DELETE RESTRICT,
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    billing_mode TEXT NOT NULL CHECK (billing_mode IN ('online', 'batch')),
    operation TEXT NOT NULL CHECK (operation IN ('rag_query', 'rag_reindex', 'memory_propose', 'memory_search')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT embedding_invocations_actor_fk FOREIGN KEY (organization_id, actor_role_id)
        REFERENCES organization_roles (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX embedding_invocations_organization_idx ON embedding_invocations (organization_id, created_at DESC);

-- embedding_invocations is attribution/identity metadata, not a financial
-- ledger row itself (provider_wallet_events remains the actual money
-- ledger) — no immutability trigger needed here, same reasoning as
-- model_invocations, which is also plain-mutable identity/state, not the
-- append-only ledger.

ALTER TABLE provider_wallet_events ALTER COLUMN invocation_id DROP NOT NULL;
ALTER TABLE provider_wallet_events ADD COLUMN embedding_invocation_id BIGINT REFERENCES embedding_invocations (id) ON DELETE RESTRICT;
ALTER TABLE provider_wallet_events ADD CONSTRAINT provider_wallet_events_exactly_one_invocation
    CHECK ((invocation_id IS NOT NULL) <> (embedding_invocation_id IS NOT NULL));

-- Every constraint/index that used to assume invocation_id was always
-- present is split into a chat-path and an embedding-path version, each
-- scoped with a WHERE clause — a plain UNIQUE(provider_id, invocation_id,
-- kind) does NOT protect the embedding path, because invocation_id is NULL
-- for every embedding row and NULL never conflicts with NULL in a unique
-- constraint.
ALTER TABLE provider_wallet_events DROP CONSTRAINT provider_wallet_events_unique_kind;
CREATE UNIQUE INDEX provider_wallet_events_unique_kind_chat_idx
    ON provider_wallet_events (provider_id, invocation_id, kind) WHERE invocation_id IS NOT NULL;
CREATE UNIQUE INDEX provider_wallet_events_unique_kind_embedding_idx
    ON provider_wallet_events (provider_id, embedding_invocation_id, kind) WHERE embedding_invocation_id IS NOT NULL;

DROP INDEX provider_wallet_events_one_terminal_idx;
CREATE UNIQUE INDEX provider_wallet_events_one_terminal_chat_idx
    ON provider_wallet_events (provider_id, invocation_id) WHERE invocation_id IS NOT NULL AND kind IN ('committed', 'released');
CREATE UNIQUE INDEX provider_wallet_events_one_terminal_embedding_idx
    ON provider_wallet_events (provider_id, embedding_invocation_id) WHERE embedding_invocation_id IS NOT NULL AND kind IN ('committed', 'released');

DROP INDEX provider_wallet_events_invocation_idx;
CREATE INDEX provider_wallet_events_invocation_idx
    ON provider_wallet_events (provider_id, invocation_id) WHERE invocation_id IS NOT NULL;
CREATE INDEX provider_wallet_events_embedding_invocation_idx
    ON provider_wallet_events (provider_id, embedding_invocation_id) WHERE embedding_invocation_id IS NOT NULL;
