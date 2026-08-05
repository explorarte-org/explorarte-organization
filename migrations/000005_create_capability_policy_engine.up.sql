CREATE TABLE authorization_requests (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    capability_matrix_hash TEXT NOT NULL CHECK (capability_matrix_hash ~ '^[0-9a-f]{64}$'),
    requester_role_id TEXT NOT NULL,
    capability_id TEXT NOT NULL CHECK (length(trim(capability_id)) BETWEEN 1 AND 160),
    risk TEXT NOT NULL CHECK (length(trim(risk)) BETWEEN 1 AND 32),
    approval_mode TEXT NOT NULL CHECK (approval_mode IN ('owner','policy_or_human','owner_or_cell_policy')),
    resource_type TEXT NOT NULL CHECK (length(trim(resource_type)) BETWEEN 1 AND 80),
    resource_id TEXT NOT NULL CHECK (length(trim(resource_id)) BETWEEN 1 AND 240),
    action_digest TEXT NOT NULL CHECK (action_digest ~ '^[0-9a-f]{64}$'),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled','expired','consumed')),
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 2000),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    correlation_id TEXT CHECK (correlation_id IS NULL OR length(trim(correlation_id)) BETWEEN 1 AND 200),
    causation_id TEXT CHECK (causation_id IS NULL OR length(trim(causation_id)) BETWEEN 1 AND 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ,
    rejected_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    UNIQUE (id, organization_id),
    UNIQUE (organization_id, idempotency_key),
    CONSTRAINT authorization_requests_organization_fk
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
    CONSTRAINT authorization_requests_requester_role_fk
        FOREIGN KEY (organization_id, requester_role_id)
        REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK (expires_at > created_at),
    CHECK (updated_at >= created_at),
    CHECK (
        (status = 'pending' AND approved_at IS NULL AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NULL AND consumed_at IS NULL)
        OR
        (status = 'approved' AND approved_at IS NOT NULL AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NULL AND consumed_at IS NULL)
        OR
        (status = 'rejected' AND approved_at IS NULL AND rejected_at IS NOT NULL AND cancelled_at IS NULL AND expired_at IS NULL AND consumed_at IS NULL)
        OR
        (status = 'cancelled' AND rejected_at IS NULL AND cancelled_at IS NOT NULL AND expired_at IS NULL AND consumed_at IS NULL)
        OR
        (status = 'expired' AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NOT NULL AND consumed_at IS NULL)
        OR
        (status = 'consumed' AND approved_at IS NOT NULL AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NULL AND consumed_at IS NOT NULL)
    ),
    CHECK (approved_at IS NULL OR approved_at >= created_at),
    CHECK (rejected_at IS NULL OR rejected_at >= created_at),
    CHECK (cancelled_at IS NULL OR cancelled_at >= created_at),
    CHECK (expired_at IS NULL OR expired_at >= created_at),
    CHECK (consumed_at IS NULL OR (approved_at IS NOT NULL AND consumed_at >= approved_at))
);

CREATE INDEX authorization_requests_status_expiry_idx
    ON authorization_requests (status, expires_at, id)
    WHERE status IN ('pending','approved');

CREATE INDEX authorization_requests_requester_idx
    ON authorization_requests (organization_id, requester_role_id, created_at DESC, id DESC);

CREATE INDEX authorization_requests_capability_idx
    ON authorization_requests (organization_id, capability_id, created_at DESC, id DESC);

CREATE TABLE authorization_decisions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approve','reject')),
    decided_by_role_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 2000),
    reference TEXT CHECK (reference IS NULL OR length(reference) BETWEEN 1 AND 500),
    digest TEXT CHECK (digest IS NULL OR digest ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (request_id),
    CONSTRAINT authorization_decisions_request_fk
        FOREIGN KEY (request_id, organization_id)
        REFERENCES authorization_requests(id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT authorization_decisions_actor_fk
        FOREIGN KEY (organization_id, decided_by_role_id)
        REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX authorization_decisions_actor_idx
    ON authorization_decisions (organization_id, decided_by_role_id, created_at DESC);

CREATE TABLE authorization_uses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    consumed_by_role_id TEXT NOT NULL,
    action_digest TEXT NOT NULL CHECK (action_digest ~ '^[0-9a-f]{64}$'),
    correlation_id TEXT CHECK (correlation_id IS NULL OR length(trim(correlation_id)) BETWEEN 1 AND 200),
    causation_id TEXT CHECK (causation_id IS NULL OR length(trim(causation_id)) BETWEEN 1 AND 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (request_id),
    CONSTRAINT authorization_uses_request_fk
        FOREIGN KEY (request_id, organization_id)
        REFERENCES authorization_requests(id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT authorization_uses_consumer_fk
        FOREIGN KEY (organization_id, consumed_by_role_id)
        REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX authorization_uses_consumer_idx
    ON authorization_uses (organization_id, consumed_by_role_id, created_at DESC);
