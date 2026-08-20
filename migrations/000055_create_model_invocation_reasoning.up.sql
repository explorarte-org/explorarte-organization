-- Role reasoning: the durable justification of why a role decided what it
-- decided.
--
-- This is NOT a relaxation of the rule that hidden reasoning must never reach
-- result hashing, audit events or the outbox. That rule stands and its
-- canaries still pass. What changes is that the bytes now have one governed
-- destination instead of being dropped on the floor: a table of their own,
-- written in the same transaction as the result it explains.
--
-- The distinction is the whole design. Reasoning is ORGANIZATIONAL data -- a
-- department leader thinking aloud about a plan restates the bundle it was
-- given -- so it must never travel the paths built for material that carries
-- no secrets. It is deliberately absent from every projection the Context
-- Engine reads, because readmitting it would be the perfect way for
-- organizational context to reach an adversarial reviewer that was isolated
-- from exactly that.
--
-- No task_id or role_id column: both are already on model_invocations, one
-- join away. Copying them here would create a second place for the same fact
-- to be true, and a second place for it to be wrong.
CREATE TABLE model_invocation_reasoning (
    invocation_id BIGINT PRIMARY KEY REFERENCES model_invocations(id) ON DELETE CASCADE,
    dispatch_attempt_id BIGINT NOT NULL REFERENCES model_dispatch_attempts(id) ON DELETE CASCADE,
    -- The provider's own reasoning text, stored verbatim. It is evidence of a
    -- decision, so paraphrasing or truncating it silently would defeat the
    -- purpose; the size bound is enforced by the runtime before it gets here.
    content BYTEA NOT NULL,
    -- Lets a reader prove the stored bytes are the ones that were recorded,
    -- without reading them.
    content_hash TEXT NOT NULL,
    content_bytes INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (length(content_hash) = 64),
    CHECK (content_bytes > 0 AND content_bytes = length(content))
);

COMMENT ON TABLE model_invocation_reasoning IS
    'ORGANIZATIONAL. Durable reasoning behind one model invocation, kept for role-level traceability. Never projected into context, audit events, or the outbox.';
