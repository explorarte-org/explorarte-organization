CREATE TABLE rag_knowledge_documents (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    document_id TEXT NOT NULL,
    namespace_kind TEXT NOT NULL CHECK (namespace_kind IN ('department', 'own')),
    namespace_id TEXT NOT NULL,
    created_by_role_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, document_id),
    CONSTRAINT rag_knowledge_documents_creator_fk FOREIGN KEY (organization_id, created_by_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK (document_id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CHECK (length(trim(namespace_id)) BETWEEN 1 AND 240)
);

CREATE TABLE rag_knowledge_versions (
    organization_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    namespace_kind TEXT NOT NULL CHECK (namespace_kind IN ('department', 'own')),
    namespace_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    title TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 240),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 1048576),
    source_kind TEXT NOT NULL CHECK (source_kind IN ('research', 'operational', 'human')),
    source_reference TEXT NOT NULL CHECK (length(trim(source_reference)) BETWEEN 1 AND 500),
    source_run_ref TEXT,
    proposed_by_role_id TEXT NOT NULL,
    data_class TEXT NOT NULL CHECK (data_class IN ('public', 'organizational', 'sanitized')),
    admission_attested_by TEXT NOT NULL,
    source_boundary TEXT NOT NULL,
    admission_evidence_ref TEXT NOT NULL,
    sanitization_evidence_ref TEXT,
    admission_attested_at TIMESTAMPTZ NOT NULL,
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    supersedes_version_id TEXT,
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('candidate', 'approved', 'rejected', 'deprecated', 'archived')),
    reviewer_role_id TEXT,
    reviewed_at TIMESTAMPTZ,
    revision BIGINT NOT NULL CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, version_id),
    UNIQUE (organization_id, document_id, version),
    UNIQUE (organization_id, canonical_hash),
    CONSTRAINT rag_knowledge_versions_document_fk FOREIGN KEY (organization_id, document_id) REFERENCES rag_knowledge_documents(organization_id, document_id) ON DELETE RESTRICT,
    CONSTRAINT rag_knowledge_versions_supersedes_fk FOREIGN KEY (organization_id, supersedes_version_id) REFERENCES rag_knowledge_versions(organization_id, version_id) ON DELETE RESTRICT,
    CONSTRAINT rag_knowledge_versions_proposer_fk FOREIGN KEY (organization_id, proposed_by_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT rag_knowledge_versions_reviewer_fk FOREIGN KEY (organization_id, reviewer_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK (supersedes_version_id IS NULL OR supersedes_version_id <> version_id),
    CHECK (updated_at >= created_at),
    CHECK (admission_attested_at <= created_at),
    CHECK (length(trim(admission_attested_by)) BETWEEN 1 AND 240),
    CHECK (length(trim(source_boundary)) BETWEEN 1 AND 120),
    CHECK (length(trim(admission_evidence_ref)) BETWEEN 1 AND 500),
    CHECK ((data_class = 'sanitized' AND sanitization_evidence_ref IS NOT NULL AND length(trim(sanitization_evidence_ref)) BETWEEN 1 AND 500) OR (data_class IN ('public', 'organizational') AND sanitization_evidence_ref IS NULL)),
    CHECK ((lifecycle = 'candidate' AND reviewer_role_id IS NULL AND reviewed_at IS NULL) OR (lifecycle <> 'candidate' AND reviewer_role_id IS NOT NULL AND reviewed_at IS NOT NULL))
);

CREATE INDEX rag_knowledge_versions_namespace_idx ON rag_knowledge_versions (organization_id, namespace_kind, namespace_id, lifecycle, updated_at DESC);
CREATE INDEX rag_knowledge_versions_document_idx ON rag_knowledge_versions (organization_id, document_id, version DESC);

CREATE TABLE rag_knowledge_evidence_refs (
    organization_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    reference TEXT NOT NULL,
    digest TEXT NOT NULL,
    PRIMARY KEY (organization_id, version_id, ordinal),
    UNIQUE (organization_id, version_id, reference),
    CONSTRAINT rag_knowledge_evidence_version_fk FOREIGN KEY (organization_id, version_id) REFERENCES rag_knowledge_versions(organization_id, version_id) ON DELETE RESTRICT,
    CHECK (length(trim(reference)) BETWEEN 1 AND 500),
    CHECK (length(trim(digest)) BETWEEN 1 AND 500)
);

CREATE TABLE rag_knowledge_lifecycle_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    from_lifecycle TEXT CHECK (from_lifecycle IS NULL OR from_lifecycle IN ('candidate', 'approved', 'rejected', 'deprecated', 'archived')),
    to_lifecycle TEXT NOT NULL CHECK (to_lifecycle IN ('candidate', 'approved', 'rejected', 'deprecated', 'archived')),
    actor_role_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT rag_knowledge_lifecycle_events_version_fk FOREIGN KEY (organization_id, version_id) REFERENCES rag_knowledge_versions(organization_id, version_id) ON DELETE RESTRICT,
    CONSTRAINT rag_knowledge_lifecycle_events_actor_fk FOREIGN KEY (organization_id, actor_role_id) REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    UNIQUE (organization_id, version_id, revision),
    CHECK (length(trim(reason)) BETWEEN 1 AND 2000),
    CHECK ((revision = 1 AND from_lifecycle IS NULL AND to_lifecycle = 'candidate') OR revision > 1)
);

CREATE INDEX rag_knowledge_lifecycle_events_version_idx ON rag_knowledge_lifecycle_events (organization_id, version_id, revision);

CREATE TABLE rag_knowledge_idempotency (
    organization_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    version_id TEXT NOT NULL,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, idempotency_key),
    CONSTRAINT rag_knowledge_idempotency_version_fk FOREIGN KEY (organization_id, version_id, canonical_hash) REFERENCES rag_knowledge_versions(organization_id, version_id, canonical_hash) ON DELETE RESTRICT,
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 240)
);

CREATE TABLE rag_index_generations (
    organization_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    namespace_kind TEXT NOT NULL CHECK (namespace_kind IN ('department', 'own')),
    namespace_id TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    status TEXT NOT NULL CHECK (status IN ('building', 'active', 'superseded')),
    chunker_id TEXT NOT NULL,
    chunker_version TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    activated_at TIMESTAMPTZ,
    PRIMARY KEY (organization_id, generation_id),
    UNIQUE (organization_id, namespace_kind, namespace_id, generation),
    CHECK ((status = 'active' AND activated_at IS NOT NULL) OR (status <> 'active' AND (activated_at IS NULL OR status = 'superseded')))
);

CREATE UNIQUE INDEX rag_index_generations_active_idx ON rag_index_generations (organization_id, namespace_kind, namespace_id) WHERE status = 'active';

CREATE TABLE rag_knowledge_chunks (
    organization_id TEXT NOT NULL,
    chunk_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    version_id TEXT NOT NULL,
    chunker_id TEXT NOT NULL,
    chunker_version TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    start_offset INTEGER NOT NULL CHECK (start_offset >= 0),
    end_offset INTEGER NOT NULL CHECK (end_offset > start_offset),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 8192),
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    embedding_model_id TEXT,
    embedding_model_version TEXT,
    embedding_dimension INTEGER CHECK (embedding_dimension IS NULL OR embedding_dimension > 0),
    content_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
    PRIMARY KEY (organization_id, chunk_id),
    UNIQUE (organization_id, generation_id, version_id, ordinal),
    CONSTRAINT rag_knowledge_chunks_generation_fk FOREIGN KEY (organization_id, generation_id) REFERENCES rag_index_generations(organization_id, generation_id) ON DELETE RESTRICT,
    CONSTRAINT rag_knowledge_chunks_version_fk FOREIGN KEY (organization_id, version_id) REFERENCES rag_knowledge_versions(organization_id, version_id) ON DELETE RESTRICT,
    CHECK ((embedding_model_id IS NULL AND embedding_model_version IS NULL AND embedding_dimension IS NULL) OR (embedding_model_id IS NOT NULL AND embedding_model_version IS NOT NULL AND embedding_dimension IS NOT NULL))
);

CREATE INDEX rag_knowledge_chunks_tsv_idx ON rag_knowledge_chunks USING GIN (content_tsv);
CREATE INDEX rag_knowledge_chunks_generation_idx ON rag_knowledge_chunks (organization_id, generation_id, version_id, ordinal);

CREATE OR REPLACE FUNCTION rag_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'rag audit/identity rows are immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER rag_knowledge_documents_immutable BEFORE UPDATE OR DELETE ON rag_knowledge_documents FOR EACH ROW EXECUTE FUNCTION rag_reject_mutation();
CREATE TRIGGER rag_knowledge_evidence_immutable BEFORE UPDATE OR DELETE ON rag_knowledge_evidence_refs FOR EACH ROW EXECUTE FUNCTION rag_reject_mutation();
CREATE TRIGGER rag_knowledge_lifecycle_events_immutable BEFORE UPDATE OR DELETE ON rag_knowledge_lifecycle_events FOR EACH ROW EXECUTE FUNCTION rag_reject_mutation();
CREATE TRIGGER rag_knowledge_idempotency_immutable BEFORE UPDATE OR DELETE ON rag_knowledge_idempotency FOR EACH ROW EXECUTE FUNCTION rag_reject_mutation();
CREATE TRIGGER rag_knowledge_chunks_immutable BEFORE UPDATE OR DELETE ON rag_knowledge_chunks FOR EACH ROW EXECUTE FUNCTION rag_reject_mutation();

CREATE OR REPLACE FUNCTION rag_guard_version_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_document TEXT;
BEGIN
    IF NEW.lifecycle <> 'candidate' OR NEW.revision <> 1 THEN RAISE EXCEPTION 'rag knowledge version must start as an unreviewed candidate revision 1' USING ERRCODE = '23514'; END IF;
    IF NEW.supersedes_version_id IS NOT NULL THEN
        SELECT document_id INTO prior_document FROM rag_knowledge_versions WHERE organization_id = NEW.organization_id AND version_id = NEW.supersedes_version_id;
        IF prior_document IS NULL THEN RAISE EXCEPTION 'superseded knowledge version does not exist' USING ERRCODE = '23514'; END IF;
        IF prior_document <> NEW.document_id THEN RAISE EXCEPTION 'knowledge version may only supersede a version of the same document' USING ERRCODE = '23514'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER rag_version_insert_guard BEFORE INSERT ON rag_knowledge_versions FOR EACH ROW EXECUTE FUNCTION rag_guard_version_insert();

CREATE OR REPLACE FUNCTION rag_guard_version_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE event_found BOOLEAN;
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR NEW.version_id <> OLD.version_id OR NEW.document_id <> OLD.document_id OR NEW.version <> OLD.version THEN RAISE EXCEPTION 'rag knowledge version identity is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.body <> OLD.body OR NEW.title <> OLD.title OR NEW.content_hash <> OLD.content_hash OR NEW.canonical_hash <> OLD.canonical_hash THEN RAISE EXCEPTION 'rag knowledge version content is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.data_class <> OLD.data_class OR NEW.admission_attested_by <> OLD.admission_attested_by OR NEW.admission_attested_at <> OLD.admission_attested_at THEN RAISE EXCEPTION 'rag knowledge version admission provenance is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.supersedes_version_id IS DISTINCT FROM OLD.supersedes_version_id OR NEW.created_at <> OLD.created_at THEN RAISE EXCEPTION 'rag knowledge version provenance is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.revision <> OLD.revision + 1 THEN RAISE EXCEPTION 'rag knowledge version revision must advance exactly by one' USING ERRCODE = '23514'; END IF;
    IF NEW.updated_at < OLD.updated_at THEN RAISE EXCEPTION 'rag knowledge version updated_at cannot move backwards' USING ERRCODE = '23514'; END IF;
    IF NOT ((OLD.lifecycle = 'candidate' AND NEW.lifecycle IN ('approved', 'rejected'))
        OR (OLD.lifecycle = 'approved' AND NEW.lifecycle = 'deprecated')
        OR (OLD.lifecycle = 'deprecated' AND NEW.lifecycle = 'archived')
        OR (OLD.lifecycle = 'rejected' AND NEW.lifecycle = 'archived')) THEN
        RAISE EXCEPTION 'invalid rag knowledge lifecycle transition % -> %', OLD.lifecycle, NEW.lifecycle USING ERRCODE = '23514';
    END IF;
    IF OLD.lifecycle <> 'candidate' AND (NEW.reviewer_role_id IS DISTINCT FROM OLD.reviewer_role_id OR NEW.reviewed_at IS DISTINCT FROM OLD.reviewed_at) THEN RAISE EXCEPTION 'rag knowledge review provenance is immutable after review' USING ERRCODE = '23514'; END IF;
    SELECT EXISTS (SELECT 1 FROM rag_knowledge_lifecycle_events WHERE organization_id = NEW.organization_id AND version_id = NEW.version_id AND revision = NEW.revision AND from_lifecycle = OLD.lifecycle AND to_lifecycle = NEW.lifecycle AND created_at = NEW.updated_at) INTO event_found;
    IF NOT event_found THEN RAISE EXCEPTION 'rag knowledge transition requires matching audit event' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER rag_version_update_guard BEFORE UPDATE ON rag_knowledge_versions FOR EACH ROW EXECUTE FUNCTION rag_guard_version_update();

CREATE OR REPLACE FUNCTION rag_reject_version_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'rag knowledge version rows cannot be deleted' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER rag_versions_no_delete BEFORE DELETE ON rag_knowledge_versions FOR EACH ROW EXECUTE FUNCTION rag_reject_version_delete();

CREATE OR REPLACE FUNCTION rag_guard_chunk_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE version_lifecycle TEXT;
BEGIN
    SELECT lifecycle INTO version_lifecycle FROM rag_knowledge_versions WHERE organization_id = NEW.organization_id AND version_id = NEW.version_id;
    IF version_lifecycle IS DISTINCT FROM 'approved' THEN RAISE EXCEPTION 'rag can only index approved knowledge versions' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER rag_chunk_insert_guard BEFORE INSERT ON rag_knowledge_chunks FOR EACH ROW EXECUTE FUNCTION rag_guard_chunk_insert();

CREATE OR REPLACE FUNCTION rag_guard_generation_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id <> OLD.organization_id OR NEW.generation_id <> OLD.generation_id OR NEW.namespace_kind <> OLD.namespace_kind OR NEW.namespace_id <> OLD.namespace_id OR NEW.generation <> OLD.generation OR NEW.chunker_id <> OLD.chunker_id OR NEW.chunker_version <> OLD.chunker_version OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'rag index generation identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF NOT ((OLD.status = 'building' AND NEW.status = 'active') OR (OLD.status = 'active' AND NEW.status = 'superseded')) THEN
        RAISE EXCEPTION 'invalid rag index generation transition % -> %', OLD.status, NEW.status USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER rag_generation_update_guard BEFORE UPDATE ON rag_index_generations FOR EACH ROW EXECUTE FUNCTION rag_guard_generation_update();

CREATE OR REPLACE FUNCTION rag_reject_generation_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'rag index generation rows cannot be deleted' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER rag_generations_no_delete BEFORE DELETE ON rag_index_generations FOR EACH ROW EXECUTE FUNCTION rag_reject_generation_delete();
