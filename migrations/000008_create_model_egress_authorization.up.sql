CREATE TABLE model_egress_policy_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    policy_id TEXT NOT NULL,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    introduced_by_organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('materializing','materialized')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, policy_id, policy_version),
    UNIQUE (organization_id, policy_id, canonical_hash),
    UNIQUE (id, organization_id),
    UNIQUE (id, organization_id, canonical_hash),
    CHECK (length(trim(policy_id)) BETWEEN 1 AND 160),
    CHECK (policy_id ~ '^[a-z0-9]+([._-][a-z0-9]+)*$')
);

CREATE TABLE model_egress_rules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_version_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    data_classification TEXT NOT NULL CHECK (data_classification IN ('public','sanitized','organizational','secret','clinical')),
    effect TEXT NOT NULL CHECK (effect IN ('allow','deny')),
    reason_code TEXT NOT NULL,
    hard_deny BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_egress_rules_policy_fk
        FOREIGN KEY (policy_version_id, organization_id)
        REFERENCES model_egress_policy_versions(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (policy_version_id, provider_id, data_classification),
    CHECK (length(trim(provider_id)) BETWEEN 1 AND 160),
    CHECK (provider_id = '*' OR provider_id ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'),
    CHECK (length(trim(reason_code)) BETWEEN 1 AND 120),
    CHECK (reason_code ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'),
    CHECK (NOT hard_deny OR effect = 'deny'),
    CHECK (hard_deny = (data_classification IN ('secret','clinical'))),
    CHECK (NOT hard_deny OR provider_id = '*')
);

CREATE TABLE model_egress_revision_bindings (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    policy_version_id BIGINT NOT NULL,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, organization_revision_id),
    UNIQUE (organization_id, organization_revision_id, policy_version_id, canonical_hash),
    CONSTRAINT model_egress_revision_binding_policy_fk
        FOREIGN KEY (policy_version_id, organization_id, canonical_hash)
        REFERENCES model_egress_policy_versions(id, organization_id, canonical_hash)
        ON DELETE RESTRICT
);

ALTER TABLE model_invocations
    ADD COLUMN model_egress_policy_version_id BIGINT,
    ADD COLUMN model_egress_policy_hash TEXT;

ALTER TABLE model_invocations
    ADD CONSTRAINT model_invocations_egress_pair_check
        CHECK (
            (model_egress_policy_version_id IS NULL AND model_egress_policy_hash IS NULL)
            OR
            (model_egress_policy_version_id IS NOT NULL AND model_egress_policy_hash IS NOT NULL)
        ),
    ADD CONSTRAINT model_invocations_egress_hash_check
        CHECK (model_egress_policy_hash IS NULL OR model_egress_policy_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT model_invocations_egress_policy_fk
        FOREIGN KEY (model_egress_policy_version_id, organization_id, model_egress_policy_hash)
        REFERENCES model_egress_policy_versions(id, organization_id, canonical_hash)
        ON DELETE RESTRICT,
    ADD CONSTRAINT model_invocations_egress_revision_binding_fk
        FOREIGN KEY (organization_id, organization_revision_id, model_egress_policy_version_id, model_egress_policy_hash)
        REFERENCES model_egress_revision_bindings(organization_id, organization_revision_id, policy_version_id, canonical_hash)
        ON DELETE RESTRICT;

CREATE FUNCTION model_egress_normalized_classifications(value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    item JSONB;
    text_value TEXT;
    normalized JSONB;
BEGIN
    IF jsonb_typeof(value) <> 'array' OR jsonb_array_length(value) > 6 THEN
        RETURN FALSE;
    END IF;
    FOR item IN SELECT element FROM jsonb_array_elements(value) AS elements(element)
    LOOP
        IF jsonb_typeof(item) <> 'string' THEN
            RETURN FALSE;
        END IF;
        text_value := item #>> '{}';
        IF text_value NOT IN ('public','sanitized','organizational','secret','clinical','unknown') THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    SELECT COALESCE(jsonb_agg(to_jsonb(entry) ORDER BY entry), '[]'::jsonb)
      INTO normalized
      FROM (SELECT DISTINCT jsonb_array_elements_text(value) AS entry) normalized_entries;
    RETURN normalized = value;
END;
$$;

CREATE FUNCTION model_egress_normalized_reason_codes(value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    item JSONB;
    text_value TEXT;
    normalized JSONB;
BEGIN
    IF jsonb_typeof(value) <> 'array' OR jsonb_array_length(value) > 32 THEN
        RETURN FALSE;
    END IF;
    FOR item IN SELECT element FROM jsonb_array_elements(value) AS elements(element)
    LOOP
        IF jsonb_typeof(item) <> 'string' THEN
            RETURN FALSE;
        END IF;
        text_value := item #>> '{}';
        IF length(text_value) NOT BETWEEN 1 AND 120 OR text_value !~ '^[a-z0-9]+([._-][a-z0-9]+)*$' THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    SELECT COALESCE(jsonb_agg(to_jsonb(entry) ORDER BY entry), '[]'::jsonb)
      INTO normalized
      FROM (SELECT DISTINCT jsonb_array_elements_text(value) AS entry) normalized_entries;
    RETURN normalized = value;
END;
$$;

CREATE TABLE model_egress_evaluations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    invocation_id BIGINT NOT NULL,
    dispatch_attempt_id BIGINT NOT NULL,
    policy_version_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    model_profile_version_id BIGINT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_transport TEXT NOT NULL CHECK (provider_transport IN ('cli_adapter','http_adapter','fake_adapter')),
    action_digest TEXT NOT NULL CHECK (action_digest ~ '^[0-9a-f]{64}$'),
    capability_matrix_hash TEXT NOT NULL CHECK (capability_matrix_hash ~ '^[0-9a-f]{64}$'),
    context_classifications JSONB NOT NULL CHECK (model_egress_normalized_classifications(context_classifications)),
    context_classifications_hash TEXT NOT NULL CHECK (context_classifications_hash ~ '^[0-9a-f]{64}$'),
    authorization_effect TEXT NOT NULL CHECK (authorization_effect IN ('allow','deny','error')),
    authorization_reason_code TEXT NOT NULL,
    egress_effect TEXT NOT NULL CHECK (egress_effect IN ('allow','deny','not_evaluated','error')),
    egress_reason_codes JSONB NOT NULL CHECK (model_egress_normalized_reason_codes(egress_reason_codes)),
    decision_hash TEXT NOT NULL CHECK (decision_hash ~ '^[0-9a-f]{64}$'),
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_egress_evaluations_invocation_fk
        FOREIGN KEY (invocation_id, organization_id)
        REFERENCES model_invocations(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_egress_evaluations_attempt_fk
        FOREIGN KEY (dispatch_attempt_id, invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_egress_evaluations_policy_fk
        FOREIGN KEY (policy_version_id, organization_id)
        REFERENCES model_egress_policy_versions(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT model_egress_evaluations_profile_fk
        FOREIGN KEY (model_profile_version_id, organization_id)
        REFERENCES model_profile_versions(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (dispatch_attempt_id),
    CHECK (length(trim(provider_id)) BETWEEN 1 AND 160),
    CHECK (provider_id ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'),
    CHECK (length(trim(authorization_reason_code)) BETWEEN 1 AND 120),
    CHECK (authorization_reason_code ~ '^[a-z0-9]+([._-][a-z0-9]+)*$'),
    CHECK (
        (authorization_effect = 'allow' AND egress_effect IN ('allow','deny','error'))
        OR
        (authorization_effect IN ('deny','error') AND egress_effect = 'not_evaluated')
    ),
    CHECK (
        (egress_effect = 'not_evaluated' AND jsonb_array_length(egress_reason_codes) = 0)
        OR
        (egress_effect <> 'not_evaluated' AND jsonb_array_length(egress_reason_codes) > 0)
    ),
    CHECK (
        egress_effect <> 'allow'
        OR (
            jsonb_array_length(context_classifications) > 0
            AND NOT (context_classifications ?| ARRAY['secret','clinical','unknown'])
        )
    )
);

CREATE FUNCTION enforce_model_egress_policy_version_immutability() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.status = 'materializing'
       AND NEW.status = 'materialized'
       AND NEW.id = OLD.id
       AND NEW.organization_id = OLD.organization_id
       AND NEW.policy_id = OLD.policy_id
       AND NEW.policy_version = OLD.policy_version
       AND NEW.canonical_hash = OLD.canonical_hash
       AND NEW.introduced_by_organization_revision_id = OLD.introduced_by_organization_revision_id
       AND NEW.created_at = OLD.created_at THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'model egress policy versions are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION enforce_model_egress_rule_insert_window() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    parent_status TEXT;
BEGIN
    SELECT status INTO parent_status
    FROM model_egress_policy_versions
    WHERE id = NEW.policy_version_id AND organization_id = NEW.organization_id
    FOR SHARE;
    IF parent_status IS DISTINCT FROM 'materializing' THEN
        RAISE EXCEPTION 'rules may only be inserted while a policy version is materializing' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION reject_model_egress_immutable_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model egress records are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER model_egress_policy_versions_no_mutation
BEFORE UPDATE OR DELETE ON model_egress_policy_versions
FOR EACH ROW EXECUTE FUNCTION enforce_model_egress_policy_version_immutability();

CREATE TRIGGER model_egress_rules_insert_window
BEFORE INSERT ON model_egress_rules
FOR EACH ROW EXECUTE FUNCTION enforce_model_egress_rule_insert_window();

CREATE TRIGGER model_egress_rules_no_mutation
BEFORE UPDATE OR DELETE ON model_egress_rules
FOR EACH ROW EXECUTE FUNCTION reject_model_egress_immutable_mutation();

CREATE TRIGGER model_egress_revision_bindings_no_mutation
BEFORE UPDATE OR DELETE ON model_egress_revision_bindings
FOR EACH ROW EXECUTE FUNCTION reject_model_egress_immutable_mutation();

CREATE TRIGGER model_egress_evaluations_no_mutation
BEFORE UPDATE OR DELETE ON model_egress_evaluations
FOR EACH ROW EXECUTE FUNCTION reject_model_egress_immutable_mutation();

CREATE INDEX model_egress_policy_versions_revision_idx
    ON model_egress_policy_versions (organization_id, introduced_by_organization_revision_id, policy_id);
CREATE INDEX model_egress_rules_lookup_idx
    ON model_egress_rules (policy_version_id, provider_id, data_classification);
CREATE INDEX model_egress_evaluations_invocation_idx
    ON model_egress_evaluations (organization_id, invocation_id, evaluated_at DESC);

CREATE FUNCTION model_egress_revision_belongs_to_organization(p_organization_id TEXT, p_revision_id BIGINT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM organizational_units u
        WHERE u.organization_id = p_organization_id
          AND u.source_revision_id = p_revision_id
    ) OR EXISTS (
        SELECT 1
        FROM organizations o
        WHERE o.id = p_organization_id
          AND o.current_revision_id = p_revision_id
    );
$$;

ALTER TABLE model_egress_policy_versions
    ADD CONSTRAINT model_egress_policy_versions_revision_owner_check
        CHECK (model_egress_revision_belongs_to_organization(organization_id, introduced_by_organization_revision_id));
ALTER TABLE model_egress_revision_bindings
    ADD CONSTRAINT model_egress_revision_bindings_revision_owner_check
        CHECK (model_egress_revision_belongs_to_organization(organization_id, organization_revision_id));
ALTER TABLE model_egress_evaluations
    ADD CONSTRAINT model_egress_evaluations_revision_owner_check
        CHECK (model_egress_revision_belongs_to_organization(organization_id, organization_revision_id));
