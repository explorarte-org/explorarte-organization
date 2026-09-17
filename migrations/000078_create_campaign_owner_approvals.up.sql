-- CEO_CONVERSATIONAL_CAMPAIGN_REVISION_AND_OWNER_APPROVAL_V1: owner execution approvals.
--
-- Durable, append-only record of an owner explicitly approving a specific
-- campaign proposal + financial review tuple for future Executive promotion.
-- This does NOT execute the campaign or call Executive.Submit.

CREATE TABLE IF NOT EXISTS campaign_owner_approvals (
    id BIGSERIAL PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    proposal_id BIGINT NOT NULL REFERENCES campaign_proposals(id),
    proposal_canonical_hash TEXT NOT NULL CHECK (proposal_canonical_hash ~ '^[0-9a-f]{64}$'),
    financial_review_id BIGINT NOT NULL REFERENCES campaign_financial_reviews(id),
    financial_review_canonical_hash TEXT NOT NULL CHECK (financial_review_canonical_hash ~ '^[0-9a-f]{64}$'),
    approved_by_role_id TEXT NOT NULL CHECK (length(trim(approved_by_role_id)) BETWEEN 1 AND 240),
    conversation_id BIGINT REFERENCES ceo_chat_conversations(id),
    message_id BIGINT REFERENCES ceo_chat_messages(id),
    turn_task_id BIGINT REFERENCES tasks(id),
    tool_call_id TEXT NOT NULL CHECK (length(trim(tool_call_id)) BETWEEN 1 AND 240),
    status TEXT NOT NULL CHECK (status IN ('approved_for_execution')),
    execution_budget JSONB NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_approval_org_key UNIQUE (organization_id, idempotency_key),
    CONSTRAINT uq_approval_exact_tuple UNIQUE (organization_id, proposal_canonical_hash, financial_review_canonical_hash)
);

CREATE INDEX IF NOT EXISTS idx_approval_proposal ON campaign_owner_approvals(organization_id, proposal_id);
