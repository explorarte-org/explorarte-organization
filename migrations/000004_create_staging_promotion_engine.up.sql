CREATE TABLE staging_workspaces (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT NOT NULL,
    repository_id TEXT NOT NULL CHECK (repository_id ~ '^[a-z0-9]+(?:[-_][a-z0-9]+)*$'),
    repository_config_hash TEXT NOT NULL CHECK (repository_config_hash ~ '^[0-9a-f]{64}$'),
    workspace_key TEXT NOT NULL CHECK (workspace_key ~ '^[a-z0-9]+(?:[-_][a-z0-9]+)*$'),
    workspace_ref TEXT NOT NULL CHECK (workspace_ref = '' OR workspace_ref ~ '^refs/explorarte/workspaces/[0-9]+$'),
    base_commit TEXT NOT NULL CHECK (base_commit ~ '^[0-9a-f]{40,64}$'),
    target_ref TEXT NOT NULL CHECK (target_ref ~ '^refs/heads/[A-Za-z0-9][A-Za-z0-9._/-]*$'),
    status TEXT NOT NULL CHECK (status IN ('provisioning','active','sealed','abandoned','cleanup_pending','cleaned','failed')),
    holder_id TEXT NOT NULL CHECK (length(trim(holder_id)) BETWEEN 1 AND 200),
    actor_role_id TEXT NOT NULL,
    artifact_requirement_id BIGINT,
    candidate_commit TEXT CHECK (candidate_commit IS NULL OR candidate_commit ~ '^[0-9a-f]{40,64}$'),
    candidate_tree TEXT CHECK (candidate_tree IS NULL OR candidate_tree ~ '^[0-9a-f]{40,64}$'),
    manifest_digest TEXT CHECK (manifest_digest IS NULL OR manifest_digest ~ '^[0-9a-f]{64}$'),
    patch_digest TEXT CHECK (patch_digest IS NULL OR patch_digest ~ '^[0-9a-f]{64}$'),
    changed_file_count INTEGER CHECK (changed_file_count IS NULL OR changed_file_count >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    status_reason_code TEXT,
    status_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sealed_at TIMESTAMPTZ,
    abandoned_at TIMESTAMPTZ,
    cleaned_at TIMESTAMPTZ,
    CONSTRAINT staging_workspaces_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id) REFERENCES task_attempts(id, task_id) ON DELETE RESTRICT,
    CONSTRAINT staging_workspaces_artifact_requirement_fk
        FOREIGN KEY (artifact_requirement_id, task_id) REFERENCES task_requirements(id, task_id) ON DELETE RESTRICT,
    CONSTRAINT staging_workspaces_actor_role_fk
        FOREIGN KEY (organization_id, actor_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    UNIQUE (repository_id, workspace_key),
    CHECK (status_reason_code IS NULL OR length(trim(status_reason_code)) BETWEEN 1 AND 120),
    CHECK (status_reason IS NULL OR length(trim(status_reason)) BETWEEN 1 AND 4000),
    CHECK ((status = 'sealed') = (sealed_at IS NOT NULL) OR status <> 'sealed'),
    CHECK ((status = 'abandoned') = (abandoned_at IS NOT NULL) OR status <> 'abandoned'),
    CHECK ((status = 'cleaned') = (cleaned_at IS NOT NULL) OR status <> 'cleaned')
);

CREATE UNIQUE INDEX staging_workspaces_ref_unique_idx ON staging_workspaces (workspace_ref) WHERE workspace_ref <> '';

CREATE UNIQUE INDEX staging_workspaces_one_active_per_attempt_idx
    ON staging_workspaces (attempt_id)
    WHERE status IN ('provisioning','active','sealed','cleanup_pending');
CREATE INDEX staging_workspaces_status_idx ON staging_workspaces (status, updated_at, id);
CREATE INDEX staging_workspaces_task_idx ON staging_workspaces (task_id, created_at DESC, id DESC);

CREATE TABLE staging_artifacts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    digest TEXT NOT NULL UNIQUE CHECK (digest ~ '^[0-9a-f]{64}$'),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    media_type TEXT NOT NULL CHECK (length(trim(media_type)) BETWEEN 1 AND 255),
    storage_key TEXT NOT NULL UNIQUE CHECK (storage_key ~ '^artifact://sha256/[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE staging_workspace_artifacts (
    workspace_id BIGINT NOT NULL REFERENCES staging_workspaces(id) ON DELETE RESTRICT,
    artifact_id BIGINT NOT NULL REFERENCES staging_artifacts(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('manifest','binary_patch','check_report','review_report')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, artifact_id, kind),
    UNIQUE (workspace_id, kind)
);

CREATE TABLE staging_checks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES staging_workspaces(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    requirement_id BIGINT NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
    status TEXT NOT NULL CHECK (status IN ('passed','failed')),
    reference TEXT NOT NULL CHECK (length(trim(reference)) BETWEEN 1 AND 2048),
    digest TEXT CHECK (digest IS NULL OR digest ~ '^[0-9a-f]{64}$'),
    actor_role_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT staging_checks_requirement_task_fk
        FOREIGN KEY (requirement_id, task_id) REFERENCES task_requirements(id, task_id) ON DELETE RESTRICT,
    UNIQUE (workspace_id, requirement_id)
);

CREATE TABLE staging_promotions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES staging_workspaces(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    repository_id TEXT NOT NULL,
    target_ref TEXT NOT NULL,
    expected_base_commit TEXT NOT NULL CHECK (expected_base_commit ~ '^[0-9a-f]{40,64}$'),
    candidate_commit TEXT NOT NULL CHECK (candidate_commit ~ '^[0-9a-f]{40,64}$'),
    status TEXT NOT NULL CHECK (status IN ('requested','awaiting_gates','approved','applied','rejected','conflicted','cancelled','failed')),
    requested_by_role_id TEXT NOT NULL,
    approved_by_role_id TEXT,
    status_reason_code TEXT,
    status_reason TEXT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at TIMESTAMPTZ,
    applied_at TIMESTAMPTZ,
    rejected_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id),
    CHECK (status_reason_code IS NULL OR length(trim(status_reason_code)) BETWEEN 1 AND 120),
    CHECK (status_reason IS NULL OR length(trim(status_reason)) BETWEEN 1 AND 4000)
);

CREATE UNIQUE INDEX staging_promotions_one_active_per_workspace_idx
    ON staging_promotions (workspace_id)
    WHERE status IN ('requested','awaiting_gates','approved');
CREATE INDEX staging_promotions_status_idx ON staging_promotions (status, updated_at, id);

CREATE TABLE staging_reviews (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    promotion_id BIGINT NOT NULL REFERENCES staging_promotions(id) ON DELETE RESTRICT,
    requirement_id BIGINT NOT NULL REFERENCES task_requirements(id) ON DELETE RESTRICT,
    decision TEXT NOT NULL CHECK (decision IN ('approve','reject')),
    actor_role_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 4000),
    reference TEXT NOT NULL CHECK (length(trim(reference)) BETWEEN 1 AND 2048),
    digest TEXT CHECK (digest IS NULL OR digest ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (promotion_id, requirement_id, actor_role_id)
);

CREATE TABLE staging_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_type TEXT NOT NULL CHECK (aggregate_type IN ('workspace','promotion')),
    aggregate_id BIGINT NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) BETWEEN 1 AND 160),
    actor_type TEXT NOT NULL CHECK (length(trim(actor_type)) BETWEEN 1 AND 80),
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) BETWEEN 1 AND 200),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (aggregate_type, aggregate_id, sequence)
);

CREATE INDEX staging_events_lookup_idx ON staging_events (aggregate_type, aggregate_id, sequence);
