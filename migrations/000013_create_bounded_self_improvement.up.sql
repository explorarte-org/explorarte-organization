CREATE TABLE improvement_candidates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    candidate_key TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    artifact_content_hash TEXT NOT NULL CHECK (artifact_content_hash ~ '^[0-9a-f]{64}$'),
    artifact_schema_version TEXT NOT NULL,
    parent_candidate_key TEXT,
    parent_artifact_hash TEXT CHECK (parent_artifact_hash IS NULL OR parent_artifact_hash ~ '^[0-9a-f]{64}$'),
    derived_from TEXT,
    state TEXT NOT NULL CHECK (state IN ('proposed','validated','evaluating','rejected','inconclusive','approved','canary','active','deprecated','rolled_back')),
    proposed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    rollback_target_candidate_key TEXT,
    rollback_target_artifact_hash TEXT CHECK (rollback_target_artifact_hash IS NULL OR rollback_target_artifact_hash ~ '^[0-9a-f]{64}$'),
    rollback_from_state TEXT CHECK (rollback_from_state IS NULL OR rollback_from_state IN ('canary','active')),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by TEXT NOT NULL,
    UNIQUE (organization_id, candidate_key),
    UNIQUE (id, organization_id),
    CHECK (length(trim(candidate_key)) BETWEEN 1 AND 200),
    CHECK (length(trim(artifact_id)) BETWEEN 1 AND 200),
    CHECK (length(trim(artifact_schema_version)) BETWEEN 1 AND 120),
    CHECK (length(trim(created_by)) BETWEEN 1 AND 200),
    CHECK (updated_at >= proposed_at),
    CHECK ((parent_candidate_key IS NULL) = (parent_artifact_hash IS NULL)),
    CHECK (parent_candidate_key IS NULL OR length(trim(derived_from)) BETWEEN 1 AND 500),
    CHECK ((rollback_target_candidate_key IS NULL) = (rollback_target_artifact_hash IS NULL)),
    CHECK ((rollback_target_candidate_key IS NULL) = (rollback_from_state IS NULL)),
    CHECK ((state = 'rolled_back') = (rollback_target_candidate_key IS NOT NULL))
);

CREATE INDEX improvement_candidates_organization_state_idx
    ON improvement_candidates (organization_id, state, id);
CREATE INDEX improvement_candidates_lineage_idx
    ON improvement_candidates (organization_id, parent_candidate_key)
    WHERE parent_candidate_key IS NOT NULL;

CREATE TABLE improvement_promotion_decisions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    candidate_id BIGINT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('to_canary','to_active')),
    outcome TEXT NOT NULL CHECK (outcome IN ('authorized','denied')),
    reason TEXT,
    from_state TEXT NOT NULL CHECK (from_state IN ('proposed','validated','evaluating','rejected','inconclusive','approved','canary','active','deprecated','rolled_back')),
    to_state TEXT NOT NULL CHECK (to_state IN ('proposed','validated','evaluating','rejected','inconclusive','approved','canary','active','deprecated','rolled_back')),
    evaluation_suite_id TEXT NOT NULL,
    evaluation_overall_verdict TEXT NOT NULL CHECK (evaluation_overall_verdict IN ('pass','fail','inconclusive')),
    requested_at TIMESTAMPTZ NOT NULL,
    requested_by TEXT NOT NULL,
    decided_at TIMESTAMPTZ NOT NULL,
    decided_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT improvement_promotion_decisions_candidate_fk
        FOREIGN KEY (candidate_id, organization_id)
        REFERENCES improvement_candidates(id, organization_id)
        ON DELETE RESTRICT,
    CHECK (outcome <> 'denied' OR (reason IS NOT NULL AND length(trim(reason)) BETWEEN 1 AND 500)),
    CHECK (outcome = 'denied' OR reason IS NULL),
    CHECK (length(trim(evaluation_suite_id)) BETWEEN 1 AND 200),
    CHECK (decided_at >= requested_at),
    CHECK (length(trim(requested_by)) BETWEEN 1 AND 200),
    CHECK (length(trim(decided_by)) BETWEEN 1 AND 200)
);

CREATE INDEX improvement_promotion_decisions_candidate_idx
    ON improvement_promotion_decisions (candidate_id, decided_at, id);

-- State-machine and immutable-field enforcement at the database level, so a
-- direct SQL write (not just the Go Service layer) cannot perform an
-- unlisted transition such as proposed -> active, mirroring
-- internal/improvement/transitions.go's default-deny map exactly.
CREATE FUNCTION improvement_guard_candidate_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id <> OLD.organization_id
       OR NEW.candidate_key <> OLD.candidate_key
       OR NEW.artifact_id <> OLD.artifact_id
       OR NEW.artifact_content_hash <> OLD.artifact_content_hash
       OR NEW.artifact_schema_version <> OLD.artifact_schema_version
       OR NEW.parent_candidate_key IS DISTINCT FROM OLD.parent_candidate_key
       OR NEW.parent_artifact_hash IS DISTINCT FROM OLD.parent_artifact_hash
       OR NEW.derived_from IS DISTINCT FROM OLD.derived_from
       OR NEW.proposed_at <> OLD.proposed_at
       OR NEW.created_by <> OLD.created_by THEN
        RAISE EXCEPTION 'immutable candidate fields changed' USING ERRCODE = '23514';
    END IF;

    IF NEW.state <> OLD.state AND NOT (
        (OLD.state = 'proposed' AND NEW.state = 'validated')
        OR (OLD.state = 'validated' AND NEW.state = 'evaluating')
        OR (OLD.state = 'evaluating' AND NEW.state IN ('approved', 'rejected', 'inconclusive'))
        OR (OLD.state = 'approved' AND NEW.state = 'canary')
        OR (OLD.state = 'canary' AND NEW.state IN ('active', 'rolled_back'))
        OR (OLD.state = 'active' AND NEW.state IN ('deprecated', 'rolled_back'))
    ) THEN
        RAISE EXCEPTION 'invalid candidate state transition % -> %', OLD.state, NEW.state USING ERRCODE = '23514';
    END IF;

    IF NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'candidate revision must advance by exactly one' USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER improvement_candidates_update_guard
BEFORE UPDATE ON improvement_candidates
FOR EACH ROW EXECUTE FUNCTION improvement_guard_candidate_update();

-- Promotion decisions are an append-only audit trail: once recorded, a
-- decision never changes, matching decisiongraph's own immutable-event
-- tables (decision_branch_events, decision_budget_events, ...).
CREATE FUNCTION improvement_immutable_row() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'improvement audit row is immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER improvement_promotion_decisions_immutable
BEFORE UPDATE OR DELETE ON improvement_promotion_decisions
FOR EACH ROW EXECUTE FUNCTION improvement_immutable_row();
