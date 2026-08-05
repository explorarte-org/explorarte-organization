CREATE TABLE model_providers (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    id TEXT NOT NULL,
    transport TEXT NOT NULL CHECK (transport IN ('cli_adapter','http_adapter','fake_adapter')),
    adapter_status TEXT NOT NULL CHECK (adapter_status IN ('available','unavailable')),
    dispatch_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    direct_http_forbidden BOOLEAN NOT NULL DEFAULT FALSE,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at TIMESTAMPTZ,
    PRIMARY KEY (organization_id, id, organization_revision_id),
    CHECK (length(trim(id)) BETWEEN 1 AND 160),
    CHECK (NOT dispatch_enabled OR adapter_status = 'available')
);

CREATE TABLE model_profiles (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    id TEXT NOT NULL,
    policy_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, id),
    UNIQUE (organization_id, policy_id),
    UNIQUE (organization_id, id, policy_id),
    CHECK (length(trim(id)) BETWEEN 1 AND 160),
    CHECK (length(trim(policy_id)) BETWEEN 1 AND 160)
);

CREATE TABLE model_profile_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    canonical_document_hash TEXT NOT NULL CHECK (canonical_document_hash ~ '^[0-9a-f]{64}$'),
    version_hash TEXT NOT NULL CHECK (version_hash ~ '^[0-9a-f]{64}$'),
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    transport TEXT NOT NULL CHECK (transport IN ('cli_adapter','http_adapter','fake_adapter')),
    reasoning_effort TEXT,
    decision_status TEXT,
    adapter_status TEXT NOT NULL CHECK (adapter_status IN ('available','unavailable')),
    dispatch_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_profile_versions_profile_fk
        FOREIGN KEY (organization_id, profile_id)
        REFERENCES model_profiles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_profile_versions_provider_fk
        FOREIGN KEY (organization_id, provider_id, organization_revision_id)
        REFERENCES model_providers(organization_id, id, organization_revision_id)
        ON DELETE RESTRICT,
    UNIQUE (organization_id, profile_id, version_number),
    UNIQUE (organization_id, profile_id, version_hash),
    UNIQUE (id, organization_id),
    UNIQUE (id, organization_id, profile_id),
    UNIQUE (id, organization_id, profile_id, provider_id, provider_model_id),
    UNIQUE (organization_id, profile_id, organization_revision_id),
    CHECK (length(trim(provider_model_id)) BETWEEN 1 AND 240),
    CHECK (reasoning_effort IS NULL OR length(trim(reasoning_effort)) BETWEEN 1 AND 80),
    CHECK (decision_status IS NULL OR length(trim(decision_status)) BETWEEN 1 AND 160),
    CHECK (NOT dispatch_enabled OR adapter_status = 'available')
);

CREATE TABLE model_capability_snapshots (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    model_profile_version_id BIGINT NOT NULL,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(capabilities) = 'array'),
    capability_hash TEXT NOT NULL CHECK (capability_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_capability_snapshots_version_fk
        FOREIGN KEY (model_profile_version_id, organization_id)
        REFERENCES model_profile_versions(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (model_profile_version_id),
    UNIQUE (id, organization_id)
);

CREATE TABLE role_model_bindings (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    role_id TEXT NOT NULL,
    policy_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    model_profile_version_id BIGINT NOT NULL,
    binding_hash TEXT NOT NULL CHECK (binding_hash ~ '^[0-9a-f]{64}$'),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT role_model_bindings_role_fk
        FOREIGN KEY (organization_id, role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT role_model_bindings_profile_fk
        FOREIGN KEY (organization_id, profile_id, policy_id)
        REFERENCES model_profiles(organization_id, id, policy_id)
        ON DELETE RESTRICT,
    CONSTRAINT role_model_bindings_version_fk
        FOREIGN KEY (model_profile_version_id, organization_id, profile_id)
        REFERENCES model_profile_versions(id, organization_id, profile_id)
        ON DELETE RESTRICT,
    PRIMARY KEY (organization_id, organization_revision_id, role_id),
    CHECK (length(trim(policy_id)) BETWEEN 1 AND 160)
);

CREATE INDEX role_model_bindings_profile_idx
    ON role_model_bindings (organization_id, model_profile_version_id, role_id)
    WHERE active;

CREATE TABLE model_invocations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT NOT NULL,
    dispatch_actor_role_id TEXT NOT NULL,
    subject_role_id TEXT NOT NULL,
    context_snapshot_id BIGINT NOT NULL REFERENCES context_snapshots(id) ON DELETE RESTRICT,
    purpose TEXT NOT NULL,
    model_profile_id TEXT NOT NULL,
    model_profile_version_id BIGINT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    required_capabilities JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(required_capabilities) = 'array'),
    output_mode TEXT NOT NULL CHECK (output_mode IN ('text','json')),
    output_schema JSONB,
    max_output_tokens INTEGER NOT NULL CHECK (max_output_tokens BETWEEN 1 AND 1048576),
    temperature DOUBLE PRECISION CHECK (temperature IS NULL OR (temperature >= 0 AND temperature <= 2)),
    thinking_mode TEXT NOT NULL CHECK (thinking_mode IN ('disabled','opaque')),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK (status IN ('requested','claimed','send_started','response_received','succeeded','failed','cancelled','ambiguous')),
    error_code TEXT,
    cancel_requested_at TIMESTAMPTZ,
    deadline TIMESTAMPTZ NOT NULL,
    correlation_id TEXT,
    causation_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_at TIMESTAMPTZ,
    CONSTRAINT model_invocations_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_invocations_dispatch_role_fk
        FOREIGN KEY (organization_id, dispatch_actor_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_invocations_subject_role_fk
        FOREIGN KEY (organization_id, subject_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_invocations_profile_fk
        FOREIGN KEY (organization_id, model_profile_id)
        REFERENCES model_profiles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_invocations_version_fk
        FOREIGN KEY (model_profile_version_id, organization_id, model_profile_id, provider_id, provider_model_id)
        REFERENCES model_profile_versions(id, organization_id, profile_id, provider_id, provider_model_id)
        ON DELETE RESTRICT,
    UNIQUE (organization_id, idempotency_key),
    UNIQUE (id, organization_id),
    CHECK (length(trim(purpose)) BETWEEN 1 AND 4000),
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    CHECK (error_code IS NULL OR length(trim(error_code)) BETWEEN 1 AND 120),
    CHECK (correlation_id IS NULL OR length(correlation_id) <= 200),
    CHECK (causation_id IS NULL OR length(causation_id) <= 200),
    CHECK ((output_mode = 'text' AND output_schema IS NULL) OR output_mode = 'json'),
    CHECK (deadline > created_at),
    CHECK (updated_at >= created_at),
    CHECK (cancel_requested_at IS NULL OR cancel_requested_at >= created_at),
    CHECK (terminal_at IS NULL OR terminal_at >= created_at),
    CHECK ((status IN ('succeeded','failed','cancelled','ambiguous')) = (terminal_at IS NOT NULL))
);

CREATE INDEX model_invocations_dispatch_idx
    ON model_invocations (organization_id, status, deadline, id)
    WHERE status IN ('requested','claimed','send_started','response_received');
CREATE INDEX model_invocations_task_idx ON model_invocations (task_id, attempt_id, id);

CREATE TABLE model_dispatch_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invocation_id BIGINT NOT NULL REFERENCES model_invocations(id) ON DELETE RESTRICT,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    status TEXT NOT NULL CHECK (status IN ('claimed','send_started','response_received','failed_before_send','ambiguous','completed')),
    claim_token_hash TEXT NOT NULL CHECK (claim_token_hash ~ '^[0-9a-f]{64}$'),
    claimed_by TEXT NOT NULL CHECK (length(trim(claimed_by)) BETWEEN 1 AND 200),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claim_expires_at TIMESTAMPTZ NOT NULL,
    send_started_at TIMESTAMPTZ,
    response_received_at TIMESTAMPTZ,
    provider_request_id TEXT,
    provider_idempotency_key_hash TEXT CHECK (provider_idempotency_key_hash IS NULL OR provider_idempotency_key_hash ~ '^[0-9a-f]{64}$'),
    retry_safety TEXT NOT NULL CHECK (retry_safety IN ('safe_before_send','unsafe_after_send','not_retryable')),
    outcome_classification TEXT,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    UNIQUE (invocation_id, attempt_number),
    UNIQUE (id, invocation_id),
    CHECK (claim_expires_at > claimed_at),
    CHECK (provider_request_id IS NULL OR length(provider_request_id) <= 400),
    CHECK (outcome_classification IS NULL OR length(trim(outcome_classification)) BETWEEN 1 AND 160),
    CHECK (error_code IS NULL OR length(trim(error_code)) BETWEEN 1 AND 120),
    CHECK (send_started_at IS NULL OR send_started_at >= claimed_at),
    CHECK (response_received_at IS NULL OR (send_started_at IS NOT NULL AND response_received_at >= send_started_at)),
    CHECK (finished_at IS NULL OR finished_at >= claimed_at),
    CHECK (status = 'claimed' OR send_started_at IS NOT NULL OR status = 'failed_before_send'),
    CHECK (status <> 'response_received' OR response_received_at IS NOT NULL),
    CHECK (status NOT IN ('send_started','response_received','ambiguous','completed') OR provider_idempotency_key_hash IS NOT NULL),
    CHECK ((status IN ('failed_before_send','ambiguous','completed')) = (finished_at IS NOT NULL))
);

CREATE UNIQUE INDEX model_dispatch_attempts_one_active_idx
    ON model_dispatch_attempts (invocation_id)
    WHERE status IN ('claimed','send_started','response_received');
CREATE INDEX model_dispatch_attempts_reconcile_idx
    ON model_dispatch_attempts (claim_expires_at, id)
    WHERE status IN ('claimed','send_started','response_received');

CREATE TABLE model_invocation_results (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invocation_id BIGINT NOT NULL UNIQUE REFERENCES model_invocations(id) ON DELETE RESTRICT,
    dispatch_attempt_id BIGINT NOT NULL,
    output_mode TEXT NOT NULL CHECK (output_mode IN ('text','json')),
    text_output TEXT,
    json_output JSONB,
    tool_intents JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(tool_intents) = 'array'),
    response_hash TEXT NOT NULL CHECK (response_hash ~ '^[0-9a-f]{64}$'),
    response_bytes INTEGER NOT NULL CHECK (response_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_invocation_results_attempt_invocation_fk
        FOREIGN KEY (dispatch_attempt_id, invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT,
    CHECK ((output_mode = 'text' AND text_output IS NOT NULL AND json_output IS NULL)
        OR (output_mode = 'json' AND text_output IS NULL AND json_output IS NOT NULL))
);

CREATE TABLE model_invocation_usage (
    invocation_id BIGINT PRIMARY KEY REFERENCES model_invocations(id) ON DELETE RESTRICT,
    dispatch_attempt_id BIGINT NOT NULL,
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    total_tokens BIGINT NOT NULL DEFAULT 0 CHECK (total_tokens = input_tokens + output_tokens),
    provider_reported BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_invocation_usage_attempt_invocation_fk
        FOREIGN KEY (dispatch_attempt_id, invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT
);
