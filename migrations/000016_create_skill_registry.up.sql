CREATE TABLE skill_registry_skills (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    skill_id TEXT NOT NULL,
    created_by_role_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, skill_id),
    CONSTRAINT skill_registry_skills_creator_fk FOREIGN KEY (organization_id, created_by_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK (skill_id ~ '^[a-z0-9]+(-[a-z0-9]+)*$')
);

CREATE TABLE skill_registry_versions (
    organization_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    skill_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('draft', 'human_approved', 'candidate', 'active', 'suspended', 'retired')),
    manifest JSONB NOT NULL,
    source JSONB NOT NULL,
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    manifest_hash TEXT NOT NULL CHECK (manifest_hash ~ '^[0-9a-f]{64}$'),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    owner_approval JSONB,
    validation JSONB,
    activation_approval JSONB,
    supersedes_version_id TEXT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, version_id),
    UNIQUE (organization_id, skill_id, version),
    UNIQUE (organization_id, canonical_hash),
    CONSTRAINT skill_registry_versions_skill_fk FOREIGN KEY (organization_id, skill_id) REFERENCES skill_registry_skills(organization_id, skill_id) ON DELETE RESTRICT,
    CONSTRAINT skill_registry_versions_supersedes_fk FOREIGN KEY (organization_id, supersedes_version_id) REFERENCES skill_registry_versions(organization_id, version_id) ON DELETE RESTRICT,
    CHECK (supersedes_version_id IS NULL OR supersedes_version_id <> version_id),
    CHECK (updated_at >= created_at),
    CHECK ((lifecycle = 'draft' AND owner_approval IS NULL AND validation IS NULL AND activation_approval IS NULL)
        OR (lifecycle = 'human_approved' AND owner_approval IS NOT NULL AND validation IS NULL AND activation_approval IS NULL)
        OR (lifecycle = 'candidate' AND owner_approval IS NOT NULL AND validation IS NOT NULL AND activation_approval IS NULL)
        OR (lifecycle IN ('active', 'suspended') AND owner_approval IS NOT NULL AND validation IS NOT NULL AND activation_approval IS NOT NULL)
        OR (lifecycle = 'retired'))
);

CREATE INDEX skill_registry_versions_skill_idx ON skill_registry_versions (organization_id, skill_id, version DESC);
CREATE INDEX skill_registry_versions_lifecycle_idx ON skill_registry_versions (organization_id, lifecycle, updated_at DESC, version_id);

CREATE TABLE skill_registry_lifecycle_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    skill_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    from_lifecycle TEXT CHECK (from_lifecycle IS NULL OR from_lifecycle IN ('draft', 'human_approved', 'candidate', 'active', 'suspended', 'retired')),
    to_lifecycle TEXT NOT NULL CHECK (to_lifecycle IN ('draft', 'human_approved', 'candidate', 'active', 'suspended', 'retired')),
    actor_role_id TEXT NOT NULL,
    decision_ref TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT skill_registry_lifecycle_events_version_fk FOREIGN KEY (organization_id, version_id) REFERENCES skill_registry_versions(organization_id, version_id) ON DELETE RESTRICT,
    CONSTRAINT skill_registry_lifecycle_events_actor_fk FOREIGN KEY (organization_id, actor_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    UNIQUE (organization_id, version_id, revision),
    CHECK (length(trim(decision_ref)) BETWEEN 1 AND 500),
    CHECK ((revision = 1 AND from_lifecycle IS NULL AND to_lifecycle = 'draft') OR revision > 1)
);

CREATE INDEX skill_registry_lifecycle_events_version_idx ON skill_registry_lifecycle_events (organization_id, version_id, revision);

CREATE TABLE skill_registry_assignments (
    organization_id TEXT NOT NULL,
    assignment_id TEXT NOT NULL,
    role_id TEXT NOT NULL,
    skill_id TEXT NOT NULL,
    skill_version_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    capability_review_ref TEXT NOT NULL,
    assigned_by_role_id TEXT NOT NULL,
    assignment_decision_ref TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    assigned_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoke_reason TEXT,
    PRIMARY KEY (organization_id, assignment_id),
    CONSTRAINT skill_registry_assignments_role_fk FOREIGN KEY (organization_id, role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT skill_registry_assignments_version_fk FOREIGN KEY (organization_id, skill_version_id) REFERENCES skill_registry_versions(organization_id, version_id) ON DELETE RESTRICT,
    CONSTRAINT skill_registry_assignments_assigner_fk FOREIGN KEY (organization_id, assigned_by_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK (length(trim(capability_review_ref)) BETWEEN 1 AND 500),
    CHECK (length(trim(assignment_decision_ref)) BETWEEN 1 AND 500),
    CHECK (updated_at >= assigned_at),
    CHECK ((status = 'active' AND revoked_at IS NULL AND revoke_reason IS NULL)
        OR (status = 'revoked' AND revoked_at IS NOT NULL AND revoke_reason IS NOT NULL AND length(trim(revoke_reason)) BETWEEN 1 AND 240))
);

CREATE UNIQUE INDEX skill_registry_assignments_active_idx ON skill_registry_assignments (organization_id, role_id, skill_id) WHERE status = 'active';
CREATE INDEX skill_registry_assignments_role_idx ON skill_registry_assignments (organization_id, role_id, status, updated_at DESC);

CREATE TABLE skill_registry_assignment_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    assignment_id TEXT NOT NULL,
    skill_id TEXT NOT NULL,
    skill_version_id TEXT NOT NULL,
    role_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('assign', 'revoke')),
    actor_role_id TEXT NOT NULL,
    decision_ref TEXT NOT NULL,
    reason_code TEXT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT skill_registry_assignment_events_assignment_fk FOREIGN KEY (organization_id, assignment_id) REFERENCES skill_registry_assignments(organization_id, assignment_id) ON DELETE RESTRICT,
    CONSTRAINT skill_registry_assignment_events_actor_fk FOREIGN KEY (organization_id, actor_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    UNIQUE (organization_id, assignment_id, revision),
    CHECK (length(trim(decision_ref)) BETWEEN 1 AND 500),
    CHECK ((revision = 1 AND action = 'assign') OR (revision > 1 AND action = 'revoke'))
);

CREATE INDEX skill_registry_assignment_events_assignment_idx ON skill_registry_assignment_events (organization_id, assignment_id, revision);

ALTER TABLE skill_registry_versions ADD CONSTRAINT skill_registry_versions_canonical_unique UNIQUE (organization_id, version_id, canonical_hash);

CREATE TABLE skill_registry_skill_idempotency (
    organization_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    version_id TEXT NOT NULL,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, idempotency_key),
    CONSTRAINT skill_registry_skill_idempotency_version_fk FOREIGN KEY (organization_id, version_id, canonical_hash) REFERENCES skill_registry_versions(organization_id, version_id, canonical_hash) ON DELETE RESTRICT,
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240)
);

CREATE TABLE skill_registry_assignment_idempotency (
    organization_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    assignment_id TEXT NOT NULL,
    identity_hash TEXT NOT NULL CHECK (identity_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, idempotency_key),
    CONSTRAINT skill_registry_assignment_idempotency_assignment_fk FOREIGN KEY (organization_id, assignment_id) REFERENCES skill_registry_assignments(organization_id, assignment_id) ON DELETE RESTRICT,
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240)
);

CREATE OR REPLACE FUNCTION skill_registry_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'skill registry audit/identity rows are immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER skill_registry_skills_immutable BEFORE UPDATE OR DELETE ON skill_registry_skills FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_mutation();
CREATE TRIGGER skill_registry_lifecycle_events_immutable BEFORE UPDATE OR DELETE ON skill_registry_lifecycle_events FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_mutation();
CREATE TRIGGER skill_registry_assignment_events_immutable BEFORE UPDATE OR DELETE ON skill_registry_assignment_events FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_mutation();
CREATE TRIGGER skill_registry_skill_idempotency_immutable BEFORE UPDATE OR DELETE ON skill_registry_skill_idempotency FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_mutation();
CREATE TRIGGER skill_registry_assignment_idempotency_immutable BEFORE UPDATE OR DELETE ON skill_registry_assignment_idempotency FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_mutation();

CREATE OR REPLACE FUNCTION skill_registry_guard_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_skill TEXT;
BEGIN
    IF NEW.lifecycle <> 'draft' OR NEW.revision <> 1 THEN RAISE EXCEPTION 'skill registry version must start as draft revision 1' USING ERRCODE = '23514'; END IF;
    IF NEW.supersedes_version_id IS NOT NULL THEN
        SELECT skill_id INTO prior_skill FROM skill_registry_versions WHERE organization_id = NEW.organization_id AND version_id = NEW.supersedes_version_id;
        IF prior_skill IS NULL THEN RAISE EXCEPTION 'superseded skill version does not exist' USING ERRCODE = '23514'; END IF;
        IF prior_skill <> NEW.skill_id THEN RAISE EXCEPTION 'skill version may only supersede a version of the same skill' USING ERRCODE = '23514'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER skill_registry_version_insert_guard BEFORE INSERT ON skill_registry_versions FOR EACH ROW EXECUTE FUNCTION skill_registry_guard_version_insert();

CREATE OR REPLACE FUNCTION skill_registry_guard_version_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_found BOOLEAN;
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR NEW.version_id <> OLD.version_id OR NEW.skill_id <> OLD.skill_id OR NEW.version <> OLD.version THEN RAISE EXCEPTION 'skill registry version identity is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.manifest IS DISTINCT FROM OLD.manifest OR NEW.source IS DISTINCT FROM OLD.source OR NEW.content_hash <> OLD.content_hash OR NEW.manifest_hash <> OLD.manifest_hash OR NEW.canonical_hash <> OLD.canonical_hash THEN RAISE EXCEPTION 'skill registry version content is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.supersedes_version_id IS DISTINCT FROM OLD.supersedes_version_id OR NEW.created_at <> OLD.created_at THEN RAISE EXCEPTION 'skill registry version provenance is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.revision <> OLD.revision + 1 THEN RAISE EXCEPTION 'skill registry version revision must advance exactly by one' USING ERRCODE = '23514'; END IF;
    IF NEW.updated_at < OLD.updated_at THEN RAISE EXCEPTION 'skill registry version updated_at cannot move backwards' USING ERRCODE = '23514'; END IF;
    IF NOT ((OLD.lifecycle = 'draft' AND NEW.lifecycle IN ('human_approved', 'retired'))
        OR (OLD.lifecycle = 'human_approved' AND NEW.lifecycle IN ('candidate', 'retired'))
        OR (OLD.lifecycle = 'candidate' AND NEW.lifecycle IN ('active', 'retired'))
        OR (OLD.lifecycle = 'active' AND NEW.lifecycle IN ('suspended', 'retired'))
        OR (OLD.lifecycle = 'suspended' AND NEW.lifecycle IN ('active', 'retired'))) THEN
        RAISE EXCEPTION 'invalid skill registry lifecycle transition % -> %', OLD.lifecycle, NEW.lifecycle USING ERRCODE = '23514';
    END IF;
    IF OLD.owner_approval IS NOT NULL AND NEW.owner_approval IS DISTINCT FROM OLD.owner_approval THEN RAISE EXCEPTION 'skill registry owner approval evidence is immutable once recorded' USING ERRCODE = '23514'; END IF;
    IF OLD.validation IS NOT NULL AND NEW.validation IS DISTINCT FROM OLD.validation THEN RAISE EXCEPTION 'skill registry validation evidence is immutable once recorded' USING ERRCODE = '23514'; END IF;
    IF OLD.activation_approval IS NOT NULL AND NEW.activation_approval IS DISTINCT FROM OLD.activation_approval THEN RAISE EXCEPTION 'skill registry activation approval evidence is immutable once recorded' USING ERRCODE = '23514'; END IF;
    SELECT EXISTS (SELECT 1 FROM skill_registry_lifecycle_events WHERE organization_id = NEW.organization_id AND version_id = NEW.version_id AND revision = NEW.revision AND from_lifecycle = OLD.lifecycle AND to_lifecycle = NEW.lifecycle AND occurred_at = NEW.updated_at) INTO event_found;
    IF NOT event_found THEN RAISE EXCEPTION 'skill registry transition requires matching audit event' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER skill_registry_version_update_guard BEFORE UPDATE ON skill_registry_versions FOR EACH ROW EXECUTE FUNCTION skill_registry_guard_version_update();

CREATE OR REPLACE FUNCTION skill_registry_reject_version_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'skill registry version rows cannot be deleted' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER skill_registry_versions_no_delete BEFORE DELETE ON skill_registry_versions FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_version_delete();

CREATE OR REPLACE FUNCTION skill_registry_guard_assignment_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status <> 'active' OR NEW.revision <> 1 THEN RAISE EXCEPTION 'skill registry assignment must start active at revision 1' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER skill_registry_assignment_insert_guard BEFORE INSERT ON skill_registry_assignments FOR EACH ROW EXECUTE FUNCTION skill_registry_guard_assignment_insert();

CREATE OR REPLACE FUNCTION skill_registry_guard_assignment_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_found BOOLEAN;
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR NEW.assignment_id <> OLD.assignment_id OR NEW.role_id <> OLD.role_id OR NEW.skill_id <> OLD.skill_id OR NEW.skill_version_id <> OLD.skill_version_id THEN RAISE EXCEPTION 'skill registry assignment identity is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.assigned_by_role_id <> OLD.assigned_by_role_id OR NEW.assignment_decision_ref <> OLD.assignment_decision_ref OR NEW.capability_review_ref <> OLD.capability_review_ref OR NEW.assigned_at <> OLD.assigned_at THEN RAISE EXCEPTION 'skill registry assignment provenance is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.revision <> OLD.revision + 1 THEN RAISE EXCEPTION 'skill registry assignment revision must advance exactly by one' USING ERRCODE = '23514'; END IF;
    IF NEW.updated_at < OLD.updated_at THEN RAISE EXCEPTION 'skill registry assignment updated_at cannot move backwards' USING ERRCODE = '23514'; END IF;
    IF OLD.status <> 'active' OR NEW.status <> 'revoked' THEN RAISE EXCEPTION 'invalid skill registry assignment transition % -> %', OLD.status, NEW.status USING ERRCODE = '23514'; END IF;
    SELECT EXISTS (SELECT 1 FROM skill_registry_assignment_events WHERE organization_id = NEW.organization_id AND assignment_id = NEW.assignment_id AND revision = NEW.revision AND action = 'revoke' AND occurred_at = NEW.updated_at) INTO event_found;
    IF NOT event_found THEN RAISE EXCEPTION 'skill registry assignment transition requires matching audit event' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER skill_registry_assignment_update_guard BEFORE UPDATE ON skill_registry_assignments FOR EACH ROW EXECUTE FUNCTION skill_registry_guard_assignment_update();

CREATE OR REPLACE FUNCTION skill_registry_reject_assignment_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'skill registry assignment rows cannot be deleted' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER skill_registry_assignments_no_delete BEFORE DELETE ON skill_registry_assignments FOR EACH ROW EXECUTE FUNCTION skill_registry_reject_assignment_delete();
