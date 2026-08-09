-- Durable inbox for agent-to-agent messages (CEO<->coordinador, coordinador
-- <->worker delegation/completion), modeled 1:1 on outbox_events'
-- claim/lease/attempt shape (migration 000003) rather than inventing a new
-- locking pattern.
CREATE TABLE agent_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    sender_role_id TEXT NOT NULL CHECK (length(trim(sender_role_id)) BETWEEN 1 AND 200),
    sender_task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    recipient_role_id TEXT NOT NULL CHECK (length(trim(recipient_role_id)) BETWEEN 1 AND 200),
    recipient_task_id BIGINT REFERENCES tasks(id) ON DELETE RESTRICT,
    correlation_id TEXT NOT NULL CHECK (length(trim(correlation_id)) BETWEEN 1 AND 200),
    causation_id TEXT NOT NULL CHECK (length(trim(causation_id)) BETWEEN 1 AND 200),
    message_type TEXT NOT NULL CHECK (message_type IN ('delegation', 'completion', 'status')),
    payload JSONB NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'delivered', 'dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    claim_token_hash TEXT CHECK (claim_token_hash IS NULL OR claim_token_hash ~ '^[0-9a-f]{64}$'),
    claimed_by TEXT CHECK (claimed_by IS NULL OR length(trim(claimed_by)) BETWEEN 1 AND 200),
    claim_expires_at TIMESTAMPTZ,
    last_error TEXT,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    CONSTRAINT agent_messages_idempotency_unique UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX agent_messages_claim_idx
    ON agent_messages (organization_id, recipient_role_id, available_at, created_at, id)
    WHERE status = 'pending';
CREATE INDEX agent_messages_claim_expiry_idx
    ON agent_messages (claim_expires_at) WHERE status = 'claimed';
-- Backs the rate-limit check in Send: how many messages has this sender
-- sent this recipient recently.
CREATE INDEX agent_messages_rate_limit_idx
    ON agent_messages (organization_id, sender_role_id, recipient_role_id, created_at);
