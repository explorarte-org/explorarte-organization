CREATE TABLE organizational_memory_versions (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    entry_key TEXT NOT NULL,
    role_id TEXT NOT NULL,
    category TEXT NOT NULL,
    problem TEXT NOT NULL,
    correction TEXT NOT NULL,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('operational', 'simulation', 'synthetic_test')),
    source_run_id BIGINT NOT NULL CHECK (source_run_id > 0),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    proposed_by_role_id TEXT NOT NULL,
    data_class TEXT NOT NULL CHECK (data_class IN ('public', 'organizational', 'sanitized')),
    admission_attested_by TEXT NOT NULL,
    source_boundary TEXT NOT NULL,
    admission_evidence_ref TEXT NOT NULL,
    sanitization_evidence_ref TEXT,
    admission_attested_at TIMESTAMPTZ NOT NULL,
    supersedes_entry_key TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, entry_key),
    UNIQUE (organization_id, canonical_hash),
    UNIQUE (organization_id, entry_key, canonical_hash),
    CONSTRAINT organizational_memory_versions_role_fk FOREIGN KEY (organization_id, role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT organizational_memory_versions_proposer_fk FOREIGN KEY (organization_id, proposed_by_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT organizational_memory_versions_supersedes_fk FOREIGN KEY (organization_id, supersedes_entry_key) REFERENCES organizational_memory_versions(organization_id, entry_key) ON DELETE RESTRICT,
    CHECK (length(trim(entry_key)) BETWEEN 1 AND 240),
    CHECK (length(trim(category)) BETWEEN 1 AND 240),
    CHECK (length(trim(problem)) BETWEEN 1 AND 16000),
    CHECK (length(trim(correction)) BETWEEN 1 AND 16000),
    CHECK (length(trim(admission_attested_by)) BETWEEN 1 AND 240),
    CHECK (length(trim(source_boundary)) BETWEEN 1 AND 120),
    CHECK (length(trim(admission_evidence_ref)) BETWEEN 1 AND 500),
    CHECK (supersedes_entry_key IS NULL OR supersedes_entry_key <> entry_key),
    CHECK (admission_attested_at <= created_at),
    CHECK ((data_class = 'sanitized' AND sanitization_evidence_ref IS NOT NULL AND length(trim(sanitization_evidence_ref)) BETWEEN 1 AND 500) OR (data_class IN ('public', 'organizational') AND sanitization_evidence_ref IS NULL))
);

CREATE INDEX organizational_memory_versions_role_idx ON organizational_memory_versions (organization_id, role_id, created_at DESC, entry_key);

CREATE TABLE organizational_memory_entries (
    organization_id TEXT NOT NULL,
    entry_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('candidate', 'approved', 'deprecated', 'archived', 'rejected')),
    reviewer_role_id TEXT,
    reviewed_at TIMESTAMPTZ,
    revision BIGINT NOT NULL CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, entry_key),
    CONSTRAINT organizational_memory_entries_version_fk FOREIGN KEY (organization_id, entry_key) REFERENCES organizational_memory_versions(organization_id, entry_key) ON DELETE RESTRICT,
    CONSTRAINT organizational_memory_entries_reviewer_fk FOREIGN KEY (organization_id, reviewer_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK ((status = 'candidate' AND reviewer_role_id IS NULL AND reviewed_at IS NULL) OR (status <> 'candidate' AND reviewer_role_id IS NOT NULL AND reviewed_at IS NOT NULL))
);

CREATE INDEX organizational_memory_entries_status_idx ON organizational_memory_entries (organization_id, status, updated_at DESC, entry_key);

CREATE TABLE organizational_memory_evidence_refs (
    organization_id TEXT NOT NULL,
    entry_key TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    reference TEXT NOT NULL,
    digest TEXT NOT NULL,
    PRIMARY KEY (organization_id, entry_key, ordinal),
    UNIQUE (organization_id, entry_key, reference),
    CONSTRAINT organizational_memory_evidence_entry_fk FOREIGN KEY (organization_id, entry_key) REFERENCES organizational_memory_versions(organization_id, entry_key) ON DELETE RESTRICT,
    CHECK (length(trim(reference)) BETWEEN 1 AND 500),
    CHECK (length(trim(digest)) BETWEEN 1 AND 500)
);

CREATE TABLE organizational_memory_state_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    entry_key TEXT NOT NULL,
    from_status TEXT CHECK (from_status IS NULL OR from_status IN ('candidate', 'approved', 'deprecated', 'archived', 'rejected')),
    to_status TEXT NOT NULL CHECK (to_status IN ('candidate', 'approved', 'deprecated', 'archived', 'rejected')),
    actor_role_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT organizational_memory_state_events_entry_fk FOREIGN KEY (organization_id, entry_key) REFERENCES organizational_memory_versions(organization_id, entry_key) ON DELETE RESTRICT,
    CONSTRAINT organizational_memory_state_events_actor_fk FOREIGN KEY (organization_id, actor_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    UNIQUE (organization_id, entry_key, revision),
    CHECK (length(trim(reason)) BETWEEN 1 AND 2000),
    CHECK ((revision = 1 AND from_status IS NULL AND to_status = 'candidate') OR revision > 1)
);

CREATE INDEX organizational_memory_state_events_entry_idx ON organizational_memory_state_events (organization_id, entry_key, revision);

CREATE TABLE organizational_memory_idempotency (
    organization_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    entry_key TEXT NOT NULL,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, idempotency_key),
    CONSTRAINT organizational_memory_idempotency_version_fk FOREIGN KEY (organization_id, entry_key, canonical_hash) REFERENCES organizational_memory_versions(organization_id, entry_key, canonical_hash) ON DELETE RESTRICT,
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240)
);

CREATE OR REPLACE FUNCTION organizational_memory_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'organizational memory audit/content rows are immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER organizational_memory_versions_immutable BEFORE UPDATE OR DELETE ON organizational_memory_versions FOR EACH ROW EXECUTE FUNCTION organizational_memory_reject_mutation();
CREATE TRIGGER organizational_memory_evidence_immutable BEFORE UPDATE OR DELETE ON organizational_memory_evidence_refs FOR EACH ROW EXECUTE FUNCTION organizational_memory_reject_mutation();
CREATE TRIGGER organizational_memory_events_immutable BEFORE UPDATE OR DELETE ON organizational_memory_state_events FOR EACH ROW EXECUTE FUNCTION organizational_memory_reject_mutation();
CREATE TRIGGER organizational_memory_idempotency_immutable BEFORE UPDATE OR DELETE ON organizational_memory_idempotency FOR EACH ROW EXECUTE FUNCTION organizational_memory_reject_mutation();

CREATE OR REPLACE FUNCTION organizational_memory_guard_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_role TEXT;
BEGIN
    IF NEW.supersedes_entry_key IS NOT NULL THEN
        SELECT role_id INTO prior_role FROM organizational_memory_versions WHERE organization_id = NEW.organization_id AND entry_key = NEW.supersedes_entry_key;
        IF prior_role IS NULL THEN RAISE EXCEPTION 'superseded organizational memory entry does not exist' USING ERRCODE = '23514'; END IF;
        IF prior_role <> NEW.role_id THEN RAISE EXCEPTION 'organizational memory may only supersede an entry in the same role namespace' USING ERRCODE = '23514'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER organizational_memory_version_insert_guard BEFORE INSERT ON organizational_memory_versions FOR EACH ROW EXECUTE FUNCTION organizational_memory_guard_version_insert();

CREATE OR REPLACE FUNCTION organizational_memory_guard_entry_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE created TIMESTAMPTZ; event_found BOOLEAN;
BEGIN
    IF NEW.status <> 'candidate' OR NEW.revision <> 1 OR NEW.reviewer_role_id IS NOT NULL OR NEW.reviewed_at IS NOT NULL THEN RAISE EXCEPTION 'organizational memory lifecycle must start as unreviewed candidate revision 1' USING ERRCODE = '23514'; END IF;
    SELECT created_at INTO created FROM organizational_memory_versions WHERE organization_id = NEW.organization_id AND entry_key = NEW.entry_key;
    IF created IS NULL OR NEW.updated_at < created THEN RAISE EXCEPTION 'organizational memory updated_at cannot predate content creation' USING ERRCODE = '23514'; END IF;
    SELECT EXISTS (SELECT 1 FROM organizational_memory_state_events WHERE organization_id=NEW.organization_id AND entry_key=NEW.entry_key AND revision=1 AND from_status IS NULL AND to_status='candidate' AND created_at=NEW.updated_at) INTO event_found;
    IF NOT event_found THEN RAISE EXCEPTION 'organizational memory candidate requires creation audit event' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER organizational_memory_entry_insert_guard BEFORE INSERT ON organizational_memory_entries FOR EACH ROW EXECUTE FUNCTION organizational_memory_guard_entry_insert();

CREATE OR REPLACE FUNCTION organizational_memory_guard_entry_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE created TIMESTAMPTZ; event_found BOOLEAN;
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR NEW.entry_key <> OLD.entry_key THEN RAISE EXCEPTION 'organizational memory identity is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.revision <> OLD.revision + 1 THEN RAISE EXCEPTION 'organizational memory revision must advance exactly by one' USING ERRCODE = '23514'; END IF;
    IF NEW.updated_at < OLD.updated_at THEN RAISE EXCEPTION 'organizational memory updated_at cannot move backwards' USING ERRCODE = '23514'; END IF;
    IF NOT ((OLD.status = 'candidate' AND NEW.status IN ('approved', 'rejected')) OR (OLD.status = 'approved' AND NEW.status = 'deprecated') OR (OLD.status = 'deprecated' AND NEW.status = 'archived') OR (OLD.status = 'rejected' AND NEW.status = 'archived')) THEN RAISE EXCEPTION 'invalid organizational memory transition % -> %', OLD.status, NEW.status USING ERRCODE = '23514'; END IF;
    IF NEW.status <> 'candidate' AND (NEW.reviewer_role_id IS NULL OR NEW.reviewed_at IS NULL) THEN RAISE EXCEPTION 'reviewed organizational memory state requires reviewer provenance' USING ERRCODE = '23514'; END IF;
    SELECT created_at INTO created FROM organizational_memory_versions WHERE organization_id=NEW.organization_id AND entry_key=NEW.entry_key;
    IF NEW.reviewed_at IS NOT NULL AND (NEW.reviewed_at < created OR NEW.reviewed_at > NEW.updated_at) THEN RAISE EXCEPTION 'organizational memory reviewed_at must be within the entry lifetime' USING ERRCODE = '23514'; END IF;
    IF OLD.status <> 'candidate' AND (NEW.reviewer_role_id IS DISTINCT FROM OLD.reviewer_role_id OR NEW.reviewed_at IS DISTINCT FROM OLD.reviewed_at) THEN RAISE EXCEPTION 'organizational memory review provenance is immutable after review' USING ERRCODE = '23514'; END IF;
    SELECT EXISTS (SELECT 1 FROM organizational_memory_state_events WHERE organization_id=NEW.organization_id AND entry_key=NEW.entry_key AND revision=NEW.revision AND from_status=OLD.status AND to_status=NEW.status AND created_at=NEW.updated_at) INTO event_found;
    IF NOT event_found THEN RAISE EXCEPTION 'organizational memory transition requires matching audit event' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER organizational_memory_entry_update_guard BEFORE UPDATE ON organizational_memory_entries FOR EACH ROW EXECUTE FUNCTION organizational_memory_guard_entry_update();

CREATE OR REPLACE FUNCTION organizational_memory_reject_entry_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'organizational memory lifecycle rows cannot be deleted' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER organizational_memory_entries_no_delete BEFORE DELETE ON organizational_memory_entries FOR EACH ROW EXECUTE FUNCTION organizational_memory_reject_entry_delete();
