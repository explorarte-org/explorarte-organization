CREATE TABLE model_execution_principals (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    principal_key TEXT NOT NULL,
    dispatch_actor_role_id TEXT NOT NULL,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('local_process','cell_process')),
    status TEXT NOT NULL CHECK (status IN ('active','disabled')),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    registered_by_role_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    disabled_at TIMESTAMPTZ,
    disabled_by_role_id TEXT,
    disable_reason_code TEXT,
    CONSTRAINT model_execution_principals_role_fk
        FOREIGN KEY (organization_id, dispatch_actor_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT,
    UNIQUE (organization_id, principal_key),
    UNIQUE (organization_id, idempotency_key),
    UNIQUE (id, organization_id),
    UNIQUE (id, organization_id, dispatch_actor_role_id),
    CHECK (length(trim(principal_key)) BETWEEN 1 AND 200),
    CHECK (principal_key ~ '^[a-zA-Z0-9]+([._/-][a-zA-Z0-9]+)*$'),
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    CHECK (length(trim(registered_by_role_id)) BETWEEN 1 AND 200),
    CHECK (updated_at >= created_at),
    CHECK (disabled_at IS NULL OR disabled_at >= created_at),
    CHECK ((status = 'active') = (disabled_at IS NULL)),
    CHECK ((status = 'active') = (disabled_by_role_id IS NULL)),
    CHECK ((status = 'active') = (disable_reason_code IS NULL)),
    CHECK (disable_reason_code IS NULL OR (length(trim(disable_reason_code)) BETWEEN 1 AND 120 AND disable_reason_code ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'))
);

CREATE INDEX model_execution_principals_status_idx
    ON model_execution_principals (organization_id, status);

CREATE TABLE model_dispatcher_assignments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT NOT NULL,
    subject_role_id TEXT NOT NULL,
    dispatch_actor_role_id TEXT NOT NULL,
    execution_principal_id BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active','exhausted','expired','revoked')),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    max_invocations INTEGER NOT NULL CHECK (max_invocations > 0 AND max_invocations <= 1000000),
    used_invocations INTEGER NOT NULL DEFAULT 0 CHECK (used_invocations >= 0),
    assignment_hash TEXT NOT NULL CHECK (assignment_hash ~ '^[0-9a-f]{64}$'),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    created_by_role_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by_role_id TEXT,
    revocation_reason_code TEXT,
    CONSTRAINT model_dispatcher_assignments_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_dispatcher_assignments_subject_role_fk
        FOREIGN KEY (organization_id, subject_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_dispatcher_assignments_dispatch_role_fk
        FOREIGN KEY (organization_id, dispatch_actor_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT model_dispatcher_assignments_principal_fk
        FOREIGN KEY (execution_principal_id, organization_id, dispatch_actor_role_id)
        REFERENCES model_execution_principals(id, organization_id, dispatch_actor_role_id)
        ON DELETE RESTRICT,
    UNIQUE (organization_id, idempotency_key),
    UNIQUE (id, organization_id),
    UNIQUE (id, organization_id, execution_principal_id),
    UNIQUE (id, organization_id, dispatch_actor_role_id),
    CHECK (used_invocations <= max_invocations),
    CHECK (valid_until > valid_from),
    CHECK (valid_from >= created_at - INTERVAL '1 minute'),
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    CHECK (length(trim(created_by_role_id)) BETWEEN 1 AND 200),
    CHECK (updated_at >= created_at),
    CHECK (terminal_at IS NULL OR terminal_at >= created_at),
    CHECK ((status IN ('exhausted','expired','revoked')) = (terminal_at IS NOT NULL)),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    CHECK ((status = 'revoked') = (revoked_by_role_id IS NOT NULL)),
    CHECK (revocation_reason_code IS NULL OR (status = 'revoked' AND length(trim(revocation_reason_code)) BETWEEN 1 AND 120 AND revocation_reason_code ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'))
);

-- At most one active assignment per organization/task/attempt (branch 10 invariant 23).
CREATE UNIQUE INDEX model_dispatcher_assignments_one_active_idx
    ON model_dispatcher_assignments (organization_id, task_id, attempt_id)
    WHERE status = 'active';

CREATE INDEX model_dispatcher_assignments_principal_idx
    ON model_dispatcher_assignments (execution_principal_id, status);
CREATE INDEX model_dispatcher_assignments_expire_idx
    ON model_dispatcher_assignments (valid_until, id)
    WHERE status = 'active';

CREATE FUNCTION reject_model_dispatcher_assignment_use_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model dispatcher assignment uses are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE TABLE model_dispatcher_assignment_uses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    assignment_id BIGINT NOT NULL,
    invocation_id BIGINT NOT NULL,
    dispatch_attempt_id BIGINT NOT NULL,
    execution_principal_id BIGINT NOT NULL,
    used_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    usage_hash TEXT NOT NULL CHECK (usage_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT model_dispatcher_assignment_uses_assignment_fk
        FOREIGN KEY (assignment_id, organization_id)
        REFERENCES model_dispatcher_assignments(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_dispatcher_assignment_uses_assignment_principal_fk
        FOREIGN KEY (assignment_id, organization_id, execution_principal_id)
        REFERENCES model_dispatcher_assignments(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_dispatcher_assignment_uses_invocation_fk
        FOREIGN KEY (invocation_id, organization_id)
        REFERENCES model_invocations(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_dispatcher_assignment_uses_attempt_fk
        FOREIGN KEY (dispatch_attempt_id, invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT,
    UNIQUE (invocation_id),
    UNIQUE (dispatch_attempt_id),
    UNIQUE (assignment_id, invocation_id),
    UNIQUE (assignment_id, dispatch_attempt_id)
);

CREATE TRIGGER model_dispatcher_assignment_uses_no_mutation
BEFORE UPDATE OR DELETE ON model_dispatcher_assignment_uses
FOR EACH ROW EXECUTE FUNCTION reject_model_dispatcher_assignment_use_mutation();

CREATE INDEX model_dispatcher_assignment_uses_principal_idx
    ON model_dispatcher_assignment_uses (execution_principal_id, used_at DESC);

-- Expand-and-contract: legacy invocations (pre branch 10) keep both columns NULL.
-- New invocations must pin both together (enforced in the service layer; the pair
-- check below only guards against a half-pinned row ever being persisted).
ALTER TABLE model_invocations
    ADD COLUMN dispatcher_assignment_id BIGINT,
    ADD COLUMN execution_principal_id BIGINT;

ALTER TABLE model_invocations
    ADD CONSTRAINT model_invocations_dispatcher_pair_check
        CHECK (
            (dispatcher_assignment_id IS NULL AND execution_principal_id IS NULL)
            OR
            (dispatcher_assignment_id IS NOT NULL AND execution_principal_id IS NOT NULL)
        ),
    ADD CONSTRAINT model_invocations_id_principal_unique
        UNIQUE (id, execution_principal_id),
    ADD CONSTRAINT model_invocations_execution_principal_fk
        FOREIGN KEY (execution_principal_id, organization_id, dispatch_actor_role_id)
        REFERENCES model_execution_principals(id, organization_id, dispatch_actor_role_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT model_invocations_dispatcher_assignment_fk
        FOREIGN KEY (dispatcher_assignment_id, organization_id, execution_principal_id)
        REFERENCES model_dispatcher_assignments(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT;

-- Legacy dispatch_attempts keep execution_principal_id NULL; new attempts must set
-- it, and when set it must match the parent invocation's pinned principal exactly.
ALTER TABLE model_dispatch_attempts
    ADD COLUMN execution_principal_id BIGINT;

ALTER TABLE model_dispatch_attempts
    ADD CONSTRAINT model_dispatch_attempts_invocation_principal_fk
        FOREIGN KEY (invocation_id, execution_principal_id)
        REFERENCES model_invocations(id, execution_principal_id)
        ON DELETE RESTRICT;
