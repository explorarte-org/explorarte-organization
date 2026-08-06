CREATE TABLE model_provider_requests (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    invocation_id BIGINT NOT NULL,
    dispatch_attempt_id BIGINT NOT NULL,
    egress_evaluation_id BIGINT NOT NULL REFERENCES model_egress_evaluations(id) ON DELETE RESTRICT,
    dispatcher_assignment_use_id BIGINT NOT NULL REFERENCES model_dispatcher_assignment_uses(id) ON DELETE RESTRICT,
    identity_assertion_id BIGINT NOT NULL REFERENCES model_execution_identity_assertions(id) ON DELETE RESTRICT,
    model_profile_id TEXT NOT NULL,
    model_profile_version_id BIGINT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    adapter_id TEXT NOT NULL,
    adapter_version INTEGER NOT NULL CHECK (adapter_version > 0),
    request_schema_version TEXT NOT NULL,
    response_schema_version TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    endpoint_fingerprint TEXT NOT NULL CHECK (endpoint_fingerprint ~ '^[0-9a-f]{64}$'),
    credential_ref_hash TEXT NOT NULL CHECK (credential_ref_hash ~ '^[0-9a-f]{64}$'),
    idempotency_key_hash TEXT NOT NULL CHECK (idempotency_key_hash ~ '^[0-9a-f]{64}$'),
    deadline TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT model_provider_requests_invocation_fk
        FOREIGN KEY (invocation_id, organization_id)
        REFERENCES model_invocations(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_provider_requests_attempt_fk
        FOREIGN KEY (dispatch_attempt_id, invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_provider_requests_provider_fk
        FOREIGN KEY (organization_id, provider_id, organization_revision_id)
        REFERENCES model_providers(organization_id, id, organization_revision_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_provider_requests_profile_fk
        FOREIGN KEY (model_profile_version_id, organization_id, model_profile_id, provider_id, provider_model_id)
        REFERENCES model_profile_versions(id, organization_id, profile_id, provider_id, provider_model_id)
        ON DELETE RESTRICT,
    UNIQUE (dispatch_attempt_id),
    UNIQUE (egress_evaluation_id),
    UNIQUE (dispatcher_assignment_use_id),
    UNIQUE (identity_assertion_id),
    UNIQUE (id, organization_id, invocation_id, dispatch_attempt_id),
    CHECK (length(trim(model_profile_id)) BETWEEN 1 AND 160),
    CHECK (length(trim(provider_model_id)) BETWEEN 1 AND 240),
    CHECK (length(trim(adapter_id)) BETWEEN 1 AND 160),
    CHECK (length(trim(request_schema_version)) BETWEEN 1 AND 120),
    CHECK (length(trim(response_schema_version)) BETWEEN 1 AND 120),
    CHECK (deadline > created_at)
);

CREATE INDEX model_provider_requests_invocation_idx
    ON model_provider_requests (invocation_id, dispatch_attempt_id);

CREATE FUNCTION reject_model_provider_request_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model provider requests are immutable';
END;
$$;

CREATE TRIGGER model_provider_requests_no_mutation
BEFORE UPDATE OR DELETE ON model_provider_requests
FOR EACH ROW EXECUTE FUNCTION reject_model_provider_request_mutation();

CREATE TABLE model_provider_outcomes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_request_record_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    invocation_id BIGINT NOT NULL,
    dispatch_attempt_id BIGINT NOT NULL,
    outcome_classification TEXT NOT NULL CHECK (
        outcome_classification IN (
            'response_received',
            'provider_rejected',
            'request_not_sent',
            'ambiguous_transport',
            'cancelled_confirmed'
        )
    ),
    provider_request_id TEXT,
    http_status INTEGER CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
    error_class TEXT,
    error_code TEXT,
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    response_hash TEXT CHECK (response_hash IS NULL OR response_hash ~ '^[0-9a-f]{64}$'),
    response_schema_version TEXT NOT NULL,
    cancellation_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT model_provider_outcomes_request_fk
        FOREIGN KEY (provider_request_record_id, organization_id, invocation_id, dispatch_attempt_id)
        REFERENCES model_provider_requests(id, organization_id, invocation_id, dispatch_attempt_id)
        ON DELETE RESTRICT,
    UNIQUE (provider_request_record_id),
    UNIQUE (dispatch_attempt_id),
    CHECK (provider_request_id IS NULL OR length(provider_request_id) <= 400),
    CHECK (error_class IS NULL OR length(trim(error_class)) BETWEEN 1 AND 120),
    CHECK (error_code IS NULL OR length(trim(error_code)) BETWEEN 1 AND 160),
    CHECK (length(trim(response_schema_version)) BETWEEN 1 AND 120),
    CHECK (outcome_classification <> 'response_received' OR (http_status BETWEEN 200 AND 299 AND response_hash IS NOT NULL AND retryable = FALSE AND cancellation_confirmed = FALSE)),
    CHECK (outcome_classification <> 'provider_rejected' OR (http_status IS NOT NULL AND response_hash IS NOT NULL AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)),
    CHECK (outcome_classification <> 'request_not_sent' OR (http_status IS NULL AND response_hash IS NULL AND provider_request_id IS NULL AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)),
    CHECK (outcome_classification <> 'ambiguous_transport' OR (http_status IS NULL AND response_hash IS NULL AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)),
    CHECK (outcome_classification <> 'cancelled_confirmed' OR (cancellation_confirmed AND retryable = FALSE))
);

CREATE FUNCTION reject_model_provider_outcome_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model provider outcomes are immutable';
END;
$$;

CREATE TRIGGER model_provider_outcomes_no_mutation
BEFORE UPDATE OR DELETE ON model_provider_outcomes
FOR EACH ROW EXECUTE FUNCTION reject_model_provider_outcome_mutation();
