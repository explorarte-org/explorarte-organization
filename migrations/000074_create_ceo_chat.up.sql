-- CEO_CONVERSATIONAL_TOOL_RUNTIME_FOUNDATION_V1: durable owner<->CEO chat.
--
-- This is chat PERSISTENCE only -- the owner-visible conversation and its
-- messages. Cognitive/tool-call trajectory for the model turn that answers
-- each owner message already has a durable home
-- (executionharness's run history + run descriptor tables); it is
-- deliberately NOT duplicated here. A message row carries only a reference
-- (task_id/attempt_id/run_id) to that trajectory, never its content.
--
-- Durable task/attempt/lease authority for each turn continues to live in
-- the existing Task Engine (tasks/task_attempts/task_leases); this
-- migration adds no second authority model.

CREATE TABLE ceo_chat_conversations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    -- The role a conversation belongs to on the owner side (e.g.
    -- empresa/human). The CEO side is fixed by the execution profile, not
    -- stored per-conversation, so this column names only who may Send.
    owner_role_id TEXT NOT NULL CHECK (length(trim(owner_role_id)) BETWEEN 1 AND 240),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, organization_id)
);

CREATE INDEX ceo_chat_conversations_org_created_idx
    ON ceo_chat_conversations (organization_id, created_at DESC, id);

CREATE TABLE ceo_chat_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    -- Monotonic per-conversation ordering, assigned by the store from
    -- max(sequence)+1 under the conversation's own row lock. Never derived
    -- from wall-clock time.
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    role TEXT NOT NULL CHECK (role IN ('owner', 'assistant')),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 65536),
    -- Required for an owner message (the durable idempotency anchor for
    -- Send); always NULL for an assistant message, which is produced by the
    -- host from a completed Harness run and never submitted twice by a
    -- caller under its own key.
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(trim(idempotency_key)) BETWEEN 1 AND 200),
    task_id BIGINT,
    attempt_id BIGINT,
    run_id TEXT CHECK (run_id IS NULL OR length(trim(run_id)) BETWEEN 1 AND 200),
    correlation_id TEXT NOT NULL CHECK (length(trim(correlation_id)) BETWEEN 1 AND 240),
    causation_id TEXT CHECK (causation_id IS NULL OR length(trim(causation_id)) BETWEEN 1 AND 240),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (conversation_id, sequence),
    -- The durable constraint the whole idempotency contract rests on: two
    -- Send calls for the same conversation and the same idempotency_key can
    -- never both insert an owner row. NULL idempotency_key (every assistant
    -- row) is exempt by ordinary SQL NULL semantics, which is exactly the
    -- rows this constraint is not about.
    UNIQUE (conversation_id, idempotency_key),
    CONSTRAINT ceo_chat_messages_conversation_org_fk
        FOREIGN KEY (conversation_id, organization_id)
        REFERENCES ceo_chat_conversations (id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT ceo_chat_messages_task_org_fk
        FOREIGN KEY (task_id, organization_id)
        REFERENCES tasks (id, organization_id) ON DELETE RESTRICT,
    CHECK (role != 'owner' OR idempotency_key IS NOT NULL),
    CHECK (role != 'owner' OR task_id IS NOT NULL),
    CHECK (role = 'owner' OR idempotency_key IS NULL)
);

CREATE INDEX ceo_chat_messages_conversation_seq_idx
    ON ceo_chat_messages (conversation_id, sequence);
CREATE INDEX ceo_chat_messages_task_idx
    ON ceo_chat_messages (task_id) WHERE task_id IS NOT NULL;
