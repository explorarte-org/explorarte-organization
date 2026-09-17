-- CEO_CONVERSATIONAL_CAMPAIGN_PROMOTION_TO_EXECUTIVE_V1: campaign promotions to Executive.
--
-- Durable record linking an approved campaign proposal + financial review + owner approval
-- tuple to an Executive root task execution.
-- One owner approval can promote to at most ONE Executive root campaign.

CREATE TABLE IF NOT EXISTS campaign_promotions (
    id BIGSERIAL PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    owner_approval_id BIGINT NOT NULL REFERENCES campaign_owner_approvals(id),
    owner_approval_canonical_hash TEXT NOT NULL CHECK (owner_approval_canonical_hash ~ '^[0-9a-f]{64}$'),
    proposal_id BIGINT NOT NULL REFERENCES campaign_proposals(id),
    proposal_canonical_hash TEXT NOT NULL CHECK (proposal_canonical_hash ~ '^[0-9a-f]{64}$'),
    financial_review_id BIGINT NOT NULL REFERENCES campaign_financial_reviews(id),
    financial_review_canonical_hash TEXT NOT NULL CHECK (financial_review_canonical_hash ~ '^[0-9a-f]{64}$'),
    execution_budget JSONB NOT NULL,
    executive_root_task_id BIGINT NOT NULL REFERENCES tasks(id),
    executive_correlation_id TEXT NOT NULL CHECK (length(trim(executive_correlation_id)) BETWEEN 1 AND 240),
    executive_submit_idempotency_key TEXT NOT NULL CHECK (length(trim(executive_submit_idempotency_key)) BETWEEN 1 AND 240),
    status TEXT NOT NULL CHECK (status IN ('submitted')),
    promoted_by_role_id TEXT NOT NULL CHECK (length(trim(promoted_by_role_id)) BETWEEN 1 AND 240),
    conversation_id BIGINT REFERENCES ceo_chat_conversations(id),
    message_id BIGINT REFERENCES ceo_chat_messages(id),
    turn_task_id BIGINT REFERENCES tasks(id),
    tool_call_id TEXT NOT NULL CHECK (length(trim(tool_call_id)) BETWEEN 1 AND 240),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_promotion_org_approval UNIQUE (organization_id, owner_approval_id),
    CONSTRAINT uq_promotion_org_root_task UNIQUE (organization_id, executive_root_task_id),
    CONSTRAINT uq_promotion_org_key UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_promotion_approval ON campaign_promotions(organization_id, owner_approval_id);
CREATE INDEX IF NOT EXISTS idx_promotion_root_task ON campaign_promotions(organization_id, executive_root_task_id);
