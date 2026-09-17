CREATE TABLE IF NOT EXISTS campaign_financial_review_requests (
    id BIGSERIAL PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    proposal_id BIGINT NOT NULL REFERENCES campaign_proposals(id),
    proposal_canonical_hash TEXT NOT NULL,
    requested_by_role_id TEXT NOT NULL,
    requested_from_conversation_id BIGINT REFERENCES ceo_chat_conversations(id),
    requested_from_message_id BIGINT REFERENCES ceo_chat_messages(id),
    requested_from_task_id BIGINT REFERENCES tasks(id),
    reviewer_role_id TEXT NOT NULL,
    review_task_id BIGINT REFERENCES tasks(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'failed')),
    idempotency_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_cfin_req_org_key UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_cfin_req_proposal ON campaign_financial_review_requests(organization_id, proposal_id);
CREATE INDEX IF NOT EXISTS idx_cfin_req_task ON campaign_financial_review_requests(organization_id, review_task_id);

CREATE TABLE IF NOT EXISTS campaign_financial_reviews (
    id BIGSERIAL PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    review_request_id BIGINT NOT NULL REFERENCES campaign_financial_review_requests(id),
    proposal_id BIGINT NOT NULL REFERENCES campaign_proposals(id),
    proposal_canonical_hash TEXT NOT NULL,
    reviewer_role_id TEXT NOT NULL,
    review_task_id BIGINT NOT NULL REFERENCES tasks(id),
    review_attempt_id BIGINT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('recommended', 'changes_requested', 'not_recommended', 'insufficient_data')),
    recommended_budget JSONB,
    estimated_cost JSONB,
    assumptions JSONB NOT NULL DEFAULT '[]'::jsonb,
    risks JSONB NOT NULL DEFAULT '[]'::jsonb,
    required_corrections JSONB NOT NULL DEFAULT '[]'::jsonb,
    missing_information JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary TEXT NOT NULL,
    canonical_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_cfin_rev_request UNIQUE (organization_id, review_request_id),
    CONSTRAINT uq_cfin_rev_task_attempt UNIQUE (organization_id, review_task_id, review_attempt_id)
);

CREATE INDEX IF NOT EXISTS idx_cfin_rev_proposal ON campaign_financial_reviews(organization_id, proposal_id);
