CREATE TABLE model_execution_identity_policy_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    policy_id TEXT NOT NULL,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    algorithm TEXT NOT NULL CHECK (algorithm = 'ed25519'),
    challenge_ttl_seconds INTEGER NOT NULL CHECK (challenge_ttl_seconds BETWEEN 30 AND 600),
    clock_skew_seconds INTEGER NOT NULL CHECK (clock_skew_seconds BETWEEN 0 AND 60),
    status TEXT NOT NULL CHECK (status IN ('active','superseded')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    superseded_at TIMESTAMPTZ,
    UNIQUE (id, organization_id),
    UNIQUE (organization_id, policy_id, policy_version),
    UNIQUE (organization_id, canonical_hash),
    CHECK ((status = 'active') = (superseded_at IS NULL))
);

CREATE UNIQUE INDEX model_execution_identity_policy_one_active_idx
    ON model_execution_identity_policy_versions (organization_id)
    WHERE status = 'active';

CREATE FUNCTION protect_model_execution_identity_policy_versions()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'model execution identity policy versions cannot be deleted';
    END IF;
    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.policy_id IS DISTINCT FROM OLD.policy_id
       OR NEW.policy_version IS DISTINCT FROM OLD.policy_version
       OR NEW.canonical_hash IS DISTINCT FROM OLD.canonical_hash
       OR NEW.algorithm IS DISTINCT FROM OLD.algorithm
       OR NEW.challenge_ttl_seconds IS DISTINCT FROM OLD.challenge_ttl_seconds
       OR NEW.clock_skew_seconds IS DISTINCT FROM OLD.clock_skew_seconds
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'model execution identity policy immutable fields cannot change';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER model_execution_identity_policy_versions_protect
BEFORE UPDATE OR DELETE ON model_execution_identity_policy_versions
FOR EACH ROW EXECUTE FUNCTION protect_model_execution_identity_policy_versions();

CREATE TABLE model_execution_identity_keys (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    execution_principal_id BIGINT NOT NULL,
    key_version INTEGER NOT NULL CHECK (key_version > 0),
    algorithm TEXT NOT NULL CHECK (algorithm = 'ed25519'),
    public_key BYTEA NOT NULL CHECK (octet_length(public_key) = 32),
    public_key_fingerprint TEXT NOT NULL CHECK (public_key_fingerprint ~ '^[0-9a-f]{64}$'),
    secret_ref TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active','retiring','retired','revoked')),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    created_by_role_id TEXT NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    valid_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    retired_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by_role_id TEXT,
    revocation_reason_code TEXT,
    CONSTRAINT model_execution_identity_keys_principal_fk
        FOREIGN KEY (execution_principal_id, organization_id)
        REFERENCES model_execution_principals(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (id, organization_id),
    UNIQUE (id, organization_id, execution_principal_id),
    UNIQUE (organization_id, execution_principal_id, key_version),
    UNIQUE (organization_id, execution_principal_id, public_key_fingerprint),
    UNIQUE (organization_id, idempotency_key),
    CHECK (length(trim(secret_ref)) BETWEEN 1 AND 500),
    CHECK (secret_ref !~ '[[:cntrl:]]'),
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    CHECK (valid_until IS NULL OR valid_until > valid_from),
    CHECK (updated_at >= created_at),
    CHECK ((status = 'retired') = (retired_at IS NOT NULL)),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    CHECK ((status = 'revoked') = (revoked_by_role_id IS NOT NULL)),
    CHECK (revocation_reason_code IS NULL OR (status = 'revoked' AND revocation_reason_code ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'))
);

CREATE UNIQUE INDEX model_execution_identity_keys_one_active_idx
    ON model_execution_identity_keys (organization_id, execution_principal_id)
    WHERE status = 'active';

CREATE INDEX model_execution_identity_keys_lookup_idx
    ON model_execution_identity_keys (organization_id, execution_principal_id, public_key_fingerprint, status);

CREATE FUNCTION protect_model_execution_identity_keys()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'model execution identity keys cannot be deleted';
    END IF;
    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.execution_principal_id IS DISTINCT FROM OLD.execution_principal_id
       OR NEW.key_version IS DISTINCT FROM OLD.key_version
       OR NEW.algorithm IS DISTINCT FROM OLD.algorithm
       OR NEW.public_key IS DISTINCT FROM OLD.public_key
       OR NEW.public_key_fingerprint IS DISTINCT FROM OLD.public_key_fingerprint
       OR NEW.secret_ref IS DISTINCT FROM OLD.secret_ref
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.request_hash IS DISTINCT FROM OLD.request_hash
       OR NEW.created_by_role_id IS DISTINCT FROM OLD.created_by_role_id
       OR NEW.valid_from IS DISTINCT FROM OLD.valid_from
       OR NEW.valid_until IS DISTINCT FROM OLD.valid_until
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'model execution identity key immutable fields cannot change';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER model_execution_identity_keys_protect
BEFORE UPDATE OR DELETE ON model_execution_identity_keys
FOR EACH ROW EXECUTE FUNCTION protect_model_execution_identity_keys();

ALTER TABLE model_invocations
    ADD COLUMN execution_identity_policy_version_id BIGINT,
    ADD COLUMN execution_identity_policy_hash TEXT;

ALTER TABLE model_invocations
    ADD CONSTRAINT model_invocations_id_organization_principal_unique
        UNIQUE (id, organization_id, execution_principal_id),
    ADD CONSTRAINT model_invocations_identity_policy_pair_check
        CHECK (
            (execution_identity_policy_version_id IS NULL AND execution_identity_policy_hash IS NULL)
            OR
            (execution_identity_policy_version_id IS NOT NULL AND execution_identity_policy_hash IS NOT NULL)
        ),
    ADD CONSTRAINT model_invocations_identity_policy_hash_check
        CHECK (execution_identity_policy_hash IS NULL OR execution_identity_policy_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT model_invocations_identity_policy_fk
        FOREIGN KEY (execution_identity_policy_version_id, organization_id)
        REFERENCES model_execution_identity_policy_versions(id, organization_id)
        ON DELETE RESTRICT;

CREATE TABLE model_execution_identity_challenges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    invocation_id BIGINT NOT NULL,
    dispatcher_assignment_id BIGINT NOT NULL,
    execution_principal_id BIGINT NOT NULL,
    execution_identity_policy_version_id BIGINT NOT NULL,
    execution_identity_policy_hash TEXT NOT NULL CHECK (execution_identity_policy_hash ~ '^[0-9a-f]{64}$'),
    execution_identity_key_id BIGINT NOT NULL,
    nonce_hash TEXT NOT NULL CHECK (nonce_hash ~ '^[0-9a-f]{64}$'),
    payload_hash TEXT NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    action_digest TEXT NOT NULL CHECK (action_digest ~ '^[0-9a-f]{64}$'),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT model_execution_identity_challenges_invocation_fk
        FOREIGN KEY (invocation_id, organization_id, execution_principal_id)
        REFERENCES model_invocations(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_execution_identity_challenges_assignment_fk
        FOREIGN KEY (dispatcher_assignment_id, organization_id, execution_principal_id)
        REFERENCES model_dispatcher_assignments(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_execution_identity_challenges_policy_fk
        FOREIGN KEY (execution_identity_policy_version_id, organization_id)
        REFERENCES model_execution_identity_policy_versions(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_execution_identity_challenges_key_fk
        FOREIGN KEY (execution_identity_key_id, organization_id, execution_principal_id)
        REFERENCES model_execution_identity_keys(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT,
    UNIQUE (id, organization_id),
    UNIQUE (organization_id, nonce_hash),
    CHECK (expires_at > issued_at),
    CHECK (consumed_at IS NULL OR consumed_at >= issued_at),
    CHECK (invalidated_at IS NULL OR invalidated_at >= issued_at),
    CHECK (NOT (consumed_at IS NOT NULL AND invalidated_at IS NOT NULL))
);

CREATE UNIQUE INDEX model_execution_identity_challenges_one_open_idx
    ON model_execution_identity_challenges (invocation_id)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

CREATE INDEX model_execution_identity_challenges_expiry_idx
    ON model_execution_identity_challenges (expires_at)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

CREATE FUNCTION protect_model_execution_identity_challenges()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'model execution identity challenges cannot be deleted';
    END IF;
    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.organization_revision_id IS DISTINCT FROM OLD.organization_revision_id
       OR NEW.invocation_id IS DISTINCT FROM OLD.invocation_id
       OR NEW.dispatcher_assignment_id IS DISTINCT FROM OLD.dispatcher_assignment_id
       OR NEW.execution_principal_id IS DISTINCT FROM OLD.execution_principal_id
       OR NEW.execution_identity_policy_version_id IS DISTINCT FROM OLD.execution_identity_policy_version_id
       OR NEW.execution_identity_policy_hash IS DISTINCT FROM OLD.execution_identity_policy_hash
       OR NEW.execution_identity_key_id IS DISTINCT FROM OLD.execution_identity_key_id
       OR NEW.nonce_hash IS DISTINCT FROM OLD.nonce_hash
       OR NEW.payload_hash IS DISTINCT FROM OLD.payload_hash
       OR NEW.action_digest IS DISTINCT FROM OLD.action_digest
       OR NEW.request_hash IS DISTINCT FROM OLD.request_hash
       OR NEW.issued_at IS DISTINCT FROM OLD.issued_at
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'model execution identity challenge immutable fields cannot change';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER model_execution_identity_challenges_protect
BEFORE UPDATE OR DELETE ON model_execution_identity_challenges
FOR EACH ROW EXECUTE FUNCTION protect_model_execution_identity_challenges();

CREATE TABLE model_execution_identity_assertions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    challenge_id BIGINT NOT NULL,
    invocation_id BIGINT NOT NULL,
    dispatch_attempt_id BIGINT NOT NULL,
    execution_principal_id BIGINT NOT NULL,
    execution_identity_key_id BIGINT NOT NULL,
    payload_hash TEXT NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    signature_hash TEXT NOT NULL CHECK (signature_hash ~ '^[0-9a-f]{64}$'),
    assertion_hash TEXT NOT NULL CHECK (assertion_hash ~ '^[0-9a-f]{64}$'),
    verification_effect TEXT NOT NULL CHECK (verification_effect IN ('allow','deny')),
    verification_reason_code TEXT NOT NULL CHECK (verification_reason_code ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'),
    verified_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT model_execution_identity_assertions_challenge_fk
        FOREIGN KEY (challenge_id, organization_id)
        REFERENCES model_execution_identity_challenges(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_execution_identity_assertions_invocation_fk
        FOREIGN KEY (invocation_id, organization_id, execution_principal_id)
        REFERENCES model_invocations(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_execution_identity_assertions_attempt_fk
        FOREIGN KEY (dispatch_attempt_id, invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_execution_identity_assertions_key_fk
        FOREIGN KEY (execution_identity_key_id, organization_id, execution_principal_id)
        REFERENCES model_execution_identity_keys(id, organization_id, execution_principal_id)
        ON DELETE RESTRICT,
    UNIQUE (challenge_id),
    UNIQUE (dispatch_attempt_id),
    UNIQUE (assertion_hash)
);

CREATE UNIQUE INDEX model_execution_identity_assertions_key_invocation_principal_idx
    ON model_execution_identity_assertions (execution_identity_key_id, invocation_id, execution_principal_id);

CREATE FUNCTION reject_model_execution_identity_assertion_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model execution identity assertions are immutable';
END;
$$;

CREATE TRIGGER model_execution_identity_assertions_no_mutation
BEFORE UPDATE OR DELETE ON model_execution_identity_assertions
FOR EACH ROW EXECUTE FUNCTION reject_model_execution_identity_assertion_mutation();

ALTER TABLE model_dispatch_attempts
    ADD COLUMN execution_identity_key_id BIGINT,
    ADD COLUMN identity_assertion_id BIGINT,
    ADD COLUMN identity_verified_at TIMESTAMPTZ;

ALTER TABLE model_dispatch_attempts
    ADD CONSTRAINT model_dispatch_attempts_identity_fields_check
        CHECK (
            (execution_identity_key_id IS NULL AND identity_assertion_id IS NULL AND identity_verified_at IS NULL)
            OR
            (execution_identity_key_id IS NOT NULL AND identity_assertion_id IS NOT NULL AND identity_verified_at IS NOT NULL)
        ),
    ADD CONSTRAINT model_dispatch_attempts_identity_key_fk
        FOREIGN KEY (execution_identity_key_id, invocation_id, execution_principal_id)
        REFERENCES model_execution_identity_assertions(execution_identity_key_id, invocation_id, execution_principal_id)
        DEFERRABLE INITIALLY DEFERRED;

-- The assertion reference is nullable for expand-and-contract legacy attempts.
ALTER TABLE model_dispatch_attempts
    ADD CONSTRAINT model_dispatch_attempts_identity_assertion_fk
        FOREIGN KEY (identity_assertion_id)
        REFERENCES model_execution_identity_assertions(id)
        DEFERRABLE INITIALLY DEFERRED;
