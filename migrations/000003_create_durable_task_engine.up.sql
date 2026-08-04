CREATE TABLE tasks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    requested_by_role_id TEXT,
    assigned_role_id TEXT NOT NULL,
    assigned_unit_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    title TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 240),
    instructions TEXT NOT NULL CHECK (length(trim(instructions)) BETWEEN 1 AND 65536),
    acceptance_criteria JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(acceptance_criteria) = 'array'),
    status TEXT NOT NULL CHECK (status IN (
        'pending', 'ready', 'leased', 'running', 'awaiting_verification', 'blocked', 'retry_wait',
        'completed', 'no_action', 'failed', 'dead_letter', 'rejected', 'cancelled'
    )),
    priority INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100000 AND 100000),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0 AND attempt_count <= max_attempts),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    correlation_id TEXT,
    causation_id TEXT,
    status_reason_code TEXT,
    status_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_at TIMESTAMPTZ,
    CONSTRAINT tasks_requested_by_role_fk
        FOREIGN KEY (organization_id, requested_by_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT tasks_assigned_role_fk
        FOREIGN KEY (organization_id, assigned_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT tasks_assigned_unit_fk
        FOREIGN KEY (organization_id, assigned_unit_id)
        REFERENCES organizational_units(organization_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT tasks_idempotency_unique UNIQUE (organization_id, idempotency_key),
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    CHECK (requested_by_role_id IS NULL OR length(trim(requested_by_role_id)) > 0),
    CHECK (length(trim(assigned_role_id)) > 0),
    CHECK (length(trim(assigned_unit_id)) > 0),
    CHECK (correlation_id IS NULL OR length(correlation_id) <= 200),
    CHECK (causation_id IS NULL OR length(causation_id) <= 200),
    CHECK (status_reason_code IS NULL OR length(trim(status_reason_code)) BETWEEN 1 AND 120),
    CHECK (status_reason IS NULL OR length(trim(status_reason)) BETWEEN 1 AND 4000),
    CHECK ((status IN ('completed', 'no_action', 'failed', 'dead_letter', 'rejected', 'cancelled')) = (terminal_at IS NOT NULL))
);

CREATE INDEX tasks_claim_order_idx
    ON tasks (priority DESC, available_at ASC, created_at ASC, id ASC)
    WHERE status = 'ready';

CREATE INDEX tasks_status_available_idx
    ON tasks (status, available_at, id)
    WHERE status IN ('pending', 'retry_wait', 'blocked');

CREATE INDEX tasks_assignee_status_idx
    ON tasks (organization_id, assigned_role_id, status, priority DESC, available_at ASC);

CREATE INDEX tasks_correlation_idx
    ON tasks (correlation_id, created_at DESC)
    WHERE correlation_id IS NOT NULL;

CREATE TABLE task_dependencies (
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (task_id, depends_on_task_id),
    CHECK (task_id <> depends_on_task_id)
);

CREATE INDEX task_dependencies_reverse_idx
    ON task_dependencies (depends_on_task_id, task_id);

CREATE TABLE task_requirements (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    requirement_key TEXT NOT NULL,
    requirement_type TEXT NOT NULL CHECK (requirement_type IN ('artifact', 'check', 'approval', 'condition', 'result')),
    description TEXT NOT NULL CHECK (length(trim(description)) BETWEEN 1 AND 4000),
    required BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'satisfied', 'failed', 'waived')),
    satisfied_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, requirement_key),
    UNIQUE (id, task_id),
    CHECK (length(trim(requirement_key)) BETWEEN 1 AND 120),
    CHECK ((status = 'satisfied') = (satisfied_at IS NOT NULL))
);

CREATE INDEX task_requirements_task_status_idx
    ON task_requirements (task_id, required, status, id);

CREATE TABLE task_evidence (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    requirement_id BIGINT,
    evidence_type TEXT NOT NULL CHECK (evidence_type IN ('artifact', 'check', 'approval', 'condition', 'result')),
    reference TEXT NOT NULL CHECK (length(trim(reference)) BETWEEN 1 AND 2048),
    digest TEXT CHECK (digest IS NULL OR digest ~ '^[0-9a-f]{64}$'),
    recorded_by TEXT NOT NULL CHECK (length(trim(recorded_by)) BETWEEN 1 AND 200),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT task_evidence_requirement_task_fk
        FOREIGN KEY (requirement_id, task_id)
        REFERENCES task_requirements(id, task_id)
        ON DELETE RESTRICT
);

CREATE INDEX task_evidence_task_idx ON task_evidence (task_id, created_at, id);
CREATE INDEX task_evidence_requirement_idx ON task_evidence (requirement_id, created_at, id) WHERE requirement_id IS NOT NULL;

CREATE TABLE task_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    state TEXT NOT NULL CHECK (state IN ('leased', 'running', 'finished', 'failed', 'lease_expired', 'cancelled')),
    worker_id TEXT NOT NULL CHECK (length(trim(worker_id)) BETWEEN 1 AND 200),
    result_summary TEXT,
    failure_code TEXT,
    retryable BOOLEAN,
    leased_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, ordinal),
    UNIQUE (id, task_id),
    CHECK (result_summary IS NULL OR length(result_summary) <= 4000),
    CHECK (failure_code IS NULL OR length(trim(failure_code)) BETWEEN 1 AND 120),
    CHECK ((state = 'running') = (started_at IS NOT NULL AND finished_at IS NULL) OR state <> 'running'),
    CHECK ((state IN ('finished', 'failed', 'lease_expired', 'cancelled')) = (finished_at IS NOT NULL))
);

CREATE INDEX task_attempts_task_idx ON task_attempts (task_id, ordinal DESC);
CREATE INDEX task_attempts_state_idx ON task_attempts (state, updated_at, id);

CREATE TABLE task_leases (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT NOT NULL,
    token_hash TEXT NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    holder_id TEXT NOT NULL CHECK (length(trim(holder_id)) BETWEEN 1 AND 200),
    status TEXT NOT NULL CHECK (status IN ('active', 'released', 'expired', 'revoked')),
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ,
    release_reason TEXT,
    CONSTRAINT task_leases_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id)
        ON DELETE RESTRICT,
    CHECK (expires_at > issued_at),
    CHECK (release_reason IS NULL OR length(trim(release_reason)) BETWEEN 1 AND 4000),
    CHECK ((status = 'active') = (released_at IS NULL))
);

CREATE UNIQUE INDEX task_leases_one_active_per_task_idx
    ON task_leases (task_id)
    WHERE status = 'active';

CREATE INDEX task_leases_expiry_idx
    ON task_leases (expires_at, id)
    WHERE status = 'active';

CREATE INDEX task_leases_attempt_idx ON task_leases (attempt_id);

CREATE TABLE task_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) BETWEEN 1 AND 160),
    from_status TEXT,
    to_status TEXT,
    actor_type TEXT NOT NULL CHECK (length(trim(actor_type)) BETWEEN 1 AND 80),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) BETWEEN 1 AND 200),
    correlation_id TEXT,
    causation_id TEXT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, sequence),
    CHECK (correlation_id IS NULL OR length(correlation_id) <= 200),
    CHECK (causation_id IS NULL OR length(causation_id) <= 200),
    CHECK (from_status IS NULL OR from_status IN (
        'pending', 'ready', 'leased', 'running', 'awaiting_verification', 'blocked', 'retry_wait',
        'completed', 'no_action', 'failed', 'dead_letter', 'rejected', 'cancelled'
    )),
    CHECK (to_status IS NULL OR to_status IN (
        'pending', 'ready', 'leased', 'running', 'awaiting_verification', 'blocked', 'retry_wait',
        'completed', 'no_action', 'failed', 'dead_letter', 'rejected', 'cancelled'
    ))
);

CREATE INDEX task_events_task_idx ON task_events (task_id, sequence);
CREATE INDEX task_events_type_idx ON task_events (event_type, occurred_at DESC);

CREATE TABLE task_dead_letters (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT,
    reason_code TEXT NOT NULL CHECK (length(trim(reason_code)) BETWEEN 1 AND 120),
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 4000),
    attempt_count INTEGER NOT NULL CHECK (attempt_count > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    redriven_at TIMESTAMPTZ,
    redrive_task_id BIGINT REFERENCES tasks(id) ON DELETE RESTRICT,
    CONSTRAINT task_dead_letters_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id)
        ON DELETE RESTRICT,
    CHECK ((redriven_at IS NULL) = (redrive_task_id IS NULL))
);

CREATE INDEX task_dead_letters_created_idx ON task_dead_letters (created_at DESC, id DESC);

CREATE TABLE outbox_events (
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

CREATE INDEX outbox_events_claim_idx
    ON outbox_events (available_at, created_at, id)
    WHERE status = 'pending';

CREATE INDEX outbox_events_recovery_idx
    ON outbox_events (claim_expires_at, id)
    WHERE status = 'claimed';

CREATE INDEX outbox_events_aggregate_idx
    ON outbox_events (aggregate_type, aggregate_id, id);
