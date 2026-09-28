-- Autonomous research agenda (investigacion). The research store in internal/search/postgres was
-- written against this schema but it was never migrated here; this creates it exactly as the store
-- reads and writes it. Findings stay candidates: nothing here publishes to an approved RAG.

CREATE TABLE research_knowledge_needs (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL,
    department_id TEXT NOT NULL CHECK (length(trim(department_id)) BETWEEN 1 AND 120),
    question TEXT NOT NULL CHECK (length(trim(question)) BETWEEN 1 AND 2000),
    description TEXT NOT NULL DEFAULT '',
    importance DOUBLE PRECISION NOT NULL CHECK (importance BETWEEN 0 AND 1),
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    last_satisfied_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX research_knowledge_needs_department_idx ON research_knowledge_needs (department_id, status);

CREATE TABLE research_topics (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL,
    department_id TEXT NOT NULL CHECK (length(trim(department_id)) BETWEEN 1 AND 120),
    parent_knowledge_need_id TEXT,
    title TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 500),
    description TEXT NOT NULL DEFAULT '',
    priority DOUBLE PRECISION NOT NULL CHECK (priority BETWEEN 0 AND 1),
    research_class TEXT NOT NULL,
    status TEXT NOT NULL,
    allowed_intents JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(allowed_intents) = 'array'),
    cadence_seconds BIGINT NOT NULL CHECK (cadence_seconds > 0),
    novelty_window_seconds BIGINT NOT NULL DEFAULT 0 CHECK (novelty_window_seconds >= 0),
    last_checked_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    mission_id TEXT,
    claim_owner TEXT,
    claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((claim_owner IS NULL) = (claim_expires_at IS NULL))
);

CREATE INDEX research_topics_due_idx ON research_topics (next_check_at, id) WHERE status = 'active';
CREATE INDEX research_topics_department_idx ON research_topics (department_id, status);

CREATE TABLE research_cycles (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL,
    topic_id TEXT NOT NULL,
    department_id TEXT NOT NULL,
    mission_id TEXT,
    trigger TEXT NOT NULL,
    outcome TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    queries_attempted INTEGER NOT NULL DEFAULT 0 CHECK (queries_attempted >= 0),
    providers_used JSONB NOT NULL DEFAULT '[]'::jsonb,
    result_count INTEGER NOT NULL DEFAULT 0 CHECK (result_count >= 0),
    new_evidence_count INTEGER NOT NULL DEFAULT 0 CHECK (new_evidence_count >= 0),
    duplicate_count INTEGER NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    findings_created INTEGER NOT NULL DEFAULT 0 CHECK (findings_created >= 0),
    rag_candidates_created INTEGER NOT NULL DEFAULT 0 CHECK (rag_candidates_created >= 0),
    error_class TEXT
);

CREATE INDEX research_cycles_topic_idx ON research_cycles (topic_id, started_at);
CREATE INDEX research_cycles_open_idx ON research_cycles (started_at) WHERE completed_at IS NULL;

CREATE TABLE research_findings (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL,
    topic_id TEXT NOT NULL,
    department_id TEXT NOT NULL,
    research_cycle_id TEXT NOT NULL REFERENCES research_cycles (id),
    title TEXT NOT NULL,
    summary TEXT NOT NULL,
    importance DOUBLE PRECISION NOT NULL CHECK (importance BETWEEN 0 AND 1),
    novelty DOUBLE PRECISION NOT NULL CHECK (novelty BETWEEN 0 AND 1),
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    classification TEXT NOT NULL,
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    mission_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX research_findings_department_idx ON research_findings (department_id, created_at);
CREATE INDEX research_findings_topic_idx ON research_findings (topic_id, created_at);

CREATE TABLE research_topic_proposals (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL,
    department_id TEXT NOT NULL,
    parent_topic_id TEXT,
    parent_need_id TEXT,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    proposed_class TEXT NOT NULL,
    proposed_priority DOUBLE PRECISION NOT NULL,
    proposed_cadence_seconds BIGINT NOT NULL,
    allowed_intents JSONB NOT NULL DEFAULT '[]'::jsonb,
    rationale TEXT NOT NULL DEFAULT '',
    origin_cycle_id TEXT,
    decision TEXT NOT NULL,
    decision_reason TEXT NOT NULL DEFAULT '',
    proposed_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ
);

CREATE INDEX research_topic_proposals_pending_idx ON research_topic_proposals (department_id, created_at)
    WHERE decision = 'pending_review';

CREATE TABLE research_query_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic_id TEXT NOT NULL,
    query TEXT NOT NULL,
    used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX research_query_records_topic_idx ON research_query_records (topic_id, used_at);

-- Same contract as outbox_events (000003): pending -> claimed -> published/dead.
CREATE TABLE research_outbox_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_type TEXT NOT NULL CHECK (length(trim(aggregate_type)) BETWEEN 1 AND 80),
    aggregate_id TEXT NOT NULL CHECK (length(trim(aggregate_id)) BETWEEN 1 AND 200),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) BETWEEN 1 AND 160),
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'published', 'dead')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    claim_token_hash TEXT CHECK (claim_token_hash IS NULL OR claim_token_hash ~ '^[0-9a-f]{64}$'),
    claimed_by TEXT,
    claim_expires_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    CHECK (claimed_by IS NULL OR length(trim(claimed_by)) BETWEEN 1 AND 200),
    CHECK (last_error IS NULL OR length(last_error) <= 4000),
    CHECK (
        (status = 'claimed' AND claim_token_hash IS NOT NULL AND claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL)
        OR
        (status <> 'claimed' AND claim_token_hash IS NULL AND claimed_by IS NULL AND claim_expires_at IS NULL)
    ),
    CHECK ((status = 'published') = (published_at IS NOT NULL))
);

CREATE INDEX research_outbox_events_claim_idx ON research_outbox_events (available_at, created_at, id)
    WHERE status = 'pending';
CREATE INDEX research_outbox_events_aggregate_idx ON research_outbox_events (aggregate_type, aggregate_id, id);
