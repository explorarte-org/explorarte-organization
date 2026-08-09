CREATE TABLE agent_budgets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    root_task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    role_id TEXT NOT NULL CHECK (length(trim(role_id)) BETWEEN 1 AND 200),
    parent_budget_id BIGINT REFERENCES agent_budgets(id) ON DELETE RESTRICT,
    max_usd_nanos BIGINT NOT NULL CHECK (max_usd_nanos > 0),
    max_tokens BIGINT NOT NULL CHECK (max_tokens > 0),
    max_model_calls BIGINT NOT NULL CHECK (max_model_calls > 0),
    max_wall_time_ms BIGINT NOT NULL CHECK (max_wall_time_ms > 0),
    max_depth BIGINT NOT NULL CHECK (max_depth > 0),
    max_retries BIGINT NOT NULL CHECK (max_retries > 0),
    max_subagents BIGINT NOT NULL CHECK (max_subagents > 0),
    used_usd_nanos BIGINT NOT NULL DEFAULT 0 CHECK (used_usd_nanos >= 0 AND used_usd_nanos <= max_usd_nanos),
    used_tokens BIGINT NOT NULL DEFAULT 0 CHECK (used_tokens >= 0 AND used_tokens <= max_tokens),
    used_model_calls BIGINT NOT NULL DEFAULT 0 CHECK (used_model_calls >= 0 AND used_model_calls <= max_model_calls),
    used_wall_time_ms BIGINT NOT NULL DEFAULT 0 CHECK (used_wall_time_ms >= 0 AND used_wall_time_ms <= max_wall_time_ms),
    depth BIGINT NOT NULL DEFAULT 1 CHECK (depth >= 1 AND depth <= max_depth),
    used_retries BIGINT NOT NULL DEFAULT 0 CHECK (used_retries >= 0 AND used_retries <= max_retries),
    used_subagents BIGINT NOT NULL DEFAULT 0 CHECK (used_subagents >= 0 AND used_subagents <= max_subagents),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (task_id)
);

CREATE INDEX agent_budgets_parent_idx ON agent_budgets (parent_budget_id);
CREATE INDEX agent_budgets_root_task_idx ON agent_budgets (root_task_id);

-- idempotency_ref means invocation_id for kind='consumed' and child task_id
-- for kind='inherited' — a retried call for the same invocation, or a
-- retried child-attachment for the same task, must apply exactly once.
CREATE TABLE agent_budget_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    budget_id BIGINT NOT NULL REFERENCES agent_budgets(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('created', 'inherited', 'consumed')),
    idempotency_ref BIGINT NOT NULL CHECK (idempotency_ref > 0),
    usd_nanos_delta BIGINT NOT NULL DEFAULT 0,
    tokens_delta BIGINT NOT NULL DEFAULT 0,
    model_calls_delta BIGINT NOT NULL DEFAULT 0,
    wall_time_ms_delta BIGINT NOT NULL DEFAULT 0,
    depth_delta BIGINT NOT NULL DEFAULT 0,
    retries_delta BIGINT NOT NULL DEFAULT 0,
    subagents_delta BIGINT NOT NULL DEFAULT 0,
    reason_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_budget_events_unique_ref UNIQUE (budget_id, kind, idempotency_ref)
);

CREATE INDEX agent_budget_events_budget_idx ON agent_budget_events (budget_id, created_at);

CREATE FUNCTION reject_agent_budget_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'agent budget events are immutable';
END;
$$;

CREATE TRIGGER agent_budget_events_no_mutation
BEFORE UPDATE OR DELETE ON agent_budget_events
FOR EACH ROW EXECUTE FUNCTION reject_agent_budget_event_mutation();
