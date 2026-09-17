-- CEO_CONVERSATIONAL_CAMPAIGN_PROPOSAL_V1: durable campaign proposals.
--
-- This is campaign PROPOSAL PERSISTENCE only -- a structured, immutable draft
-- produced by an authorized owner<->CEO conversation turn.
-- It does NOT execute the campaign, create an executive root task,
-- provision departmental tasks, or allocate operational budgets.

CREATE TABLE campaign_proposals (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    conversation_id BIGINT NOT NULL,
    created_by_role_id TEXT NOT NULL CHECK (length(trim(created_by_role_id)) BETWEEN 1 AND 240),
    created_from_message_id BIGINT NOT NULL,
    task_id BIGINT NOT NULL,
    attempt_id BIGINT NOT NULL,
    tool_call_id TEXT NOT NULL CHECK (length(trim(tool_call_id)) BETWEEN 1 AND 240),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'withdrawn')),
    title TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 4000),
    goal TEXT NOT NULL CHECK (length(trim(goal)) BETWEEN 1 AND 16000),
    acceptance_criteria JSONB NOT NULL,
    requirements JSONB NOT NULL DEFAULT '[]'::jsonb,
    budget JSONB,
    assumptions JSONB NOT NULL DEFAULT '[]'::jsonb,
    risks JSONB NOT NULL DEFAULT '[]'::jsonb,
    open_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
    financial_review_required BOOLEAN NOT NULL DEFAULT TRUE,
    execution_started BOOLEAN NOT NULL DEFAULT FALSE,
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, organization_id),
    UNIQUE (organization_id, idempotency_key),
    CONSTRAINT campaign_proposals_conversation_fk
        FOREIGN KEY (conversation_id, organization_id)
        REFERENCES ceo_chat_conversations (id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT campaign_proposals_message_fk
        FOREIGN KEY (created_from_message_id)
        REFERENCES ceo_chat_messages (id) ON DELETE RESTRICT,
    CONSTRAINT campaign_proposals_task_fk
        FOREIGN KEY (task_id, organization_id)
        REFERENCES tasks (id, organization_id) ON DELETE RESTRICT
);

CREATE INDEX campaign_proposals_org_created_idx
    ON campaign_proposals (organization_id, created_at DESC, id);
CREATE INDEX campaign_proposals_conversation_idx
    ON campaign_proposals (conversation_id, id);
CREATE INDEX campaign_proposals_task_idx
    ON campaign_proposals (task_id);
