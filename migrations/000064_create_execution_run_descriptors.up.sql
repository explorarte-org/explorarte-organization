-- MemoryOS phase 0A: immutable, metadata-only facts for one Harness run.
-- The execution event ledger remains the trajectory owner; this table records
-- the contract frozen before the first event so a later Episode projection can
-- reconstruct the run without persisting prompts, credentials, or tool bodies.
CREATE TABLE execution_run_descriptors (
    organization_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    harness_run_id        TEXT NOT NULL CHECK (length(trim(harness_run_id)) BETWEEN 1 AND 200),
    task_id               BIGINT NOT NULL,
    attempt_id            BIGINT NOT NULL,
    role_id               TEXT NOT NULL CHECK (length(trim(role_id)) BETWEEN 1 AND 240),
    execution_principal_id TEXT NOT NULL CHECK (length(trim(execution_principal_id)) BETWEEN 1 AND 240),

    context_id            TEXT NOT NULL CHECK (length(trim(context_id)) BETWEEN 1 AND 240),
    context_version       TEXT NOT NULL CHECK (length(trim(context_version)) BETWEEN 1 AND 240),
    context_digest        TEXT NOT NULL CHECK (context_digest ~ '^[0-9a-f]{64}$'),

    execution_profile_id  TEXT NOT NULL CHECK (length(trim(execution_profile_id)) BETWEEN 1 AND 240),
    model_policy_ref      TEXT NOT NULL CHECK (length(trim(model_policy_ref)) BETWEEN 1 AND 240),
    build_ref             TEXT NOT NULL DEFAULT '' CHECK (length(build_ref) <= 240),

    max_turns             INTEGER NOT NULL CHECK (max_turns > 0),
    max_tool_calls        INTEGER NOT NULL CHECK (max_tool_calls >= 0),
    frozen_tools          JSONB NOT NULL CHECK (jsonb_typeof(frozen_tools) = 'array'),
    identity_digest       TEXT NOT NULL CHECK (identity_digest ~ '^[0-9a-f]{64}$'),
    canonical_digest      TEXT NOT NULL CHECK (canonical_digest ~ '^[0-9a-f]{64}$'),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

    PRIMARY KEY (organization_id, harness_run_id),
    CONSTRAINT execution_run_descriptors_task_organization_fk
        FOREIGN KEY (task_id, organization_id)
        REFERENCES tasks(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT execution_run_descriptors_task_attempt_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id)
        ON DELETE RESTRICT
);

CREATE INDEX execution_run_descriptors_task_attempt_idx
    ON execution_run_descriptors (organization_id, task_id, attempt_id, created_at);

-- JSONB is used for the compact list of frozen tool references, but the
-- references themselves remain schema facts. Validate every object at the
-- database boundary so an ad-hoc insert cannot create an unreadable or
-- ambiguous descriptor that later projection would have to guess about.
CREATE OR REPLACE FUNCTION execution_run_descriptors_validate_tools()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    item JSONB;
    item_name TEXT;
    seen_names TEXT[] := ARRAY[]::TEXT[];
    object_keys INTEGER;
BEGIN
    FOR item IN SELECT value FROM jsonb_array_elements(NEW.frozen_tools) LOOP
        IF jsonb_typeof(item) <> 'object'
           OR jsonb_typeof(item->'name') <> 'string'
           OR jsonb_typeof(item->'definition_digest') <> 'string' THEN
            RAISE EXCEPTION 'execution run descriptor contains an invalid frozen tool reference'
                USING ERRCODE = '23514';
        END IF;
        SELECT count(*) INTO object_keys FROM jsonb_object_keys(item);
        IF object_keys <> 2 THEN
            RAISE EXCEPTION 'execution run descriptor contains unknown frozen tool fields'
                USING ERRCODE = '23514';
        END IF;
        item_name := item->>'name';
        IF item_name !~ '^[a-z][a-z0-9_.-]{0,127}$'
           OR item->>'definition_digest' !~ '^[0-9a-f]{64}$' THEN
            RAISE EXCEPTION 'execution run descriptor contains an invalid frozen tool identity'
                USING ERRCODE = '23514';
        END IF;
        IF item_name = ANY(seen_names) THEN
            RAISE EXCEPTION 'execution run descriptor contains duplicate frozen tool names'
                USING ERRCODE = '23514';
        END IF;
        seen_names := array_append(seen_names, item_name);
    END LOOP;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_run_descriptors_validate_tools
    BEFORE INSERT ON execution_run_descriptors
    FOR EACH ROW EXECUTE FUNCTION execution_run_descriptors_validate_tools();

-- Execution descriptors are historical facts. Reprojection must create a new
-- revision in a future schema; it must never rewrite or delete this row.
CREATE OR REPLACE FUNCTION execution_run_descriptors_reject_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'execution run descriptors are immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER execution_run_descriptors_immutable
    BEFORE UPDATE OR DELETE ON execution_run_descriptors
    FOR EACH ROW EXECUTE FUNCTION execution_run_descriptors_reject_mutation();
