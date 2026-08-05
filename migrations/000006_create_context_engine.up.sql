CREATE TABLE context_snapshots (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    actor_role_id TEXT NOT NULL,
    purpose TEXT NOT NULL CHECK (length(trim(purpose)) BETWEEN 1 AND 240),
    project_ref TEXT CHECK (project_ref IS NULL OR length(project_ref) BETWEEN 1 AND 240),
    task_ref TEXT CHECK (task_ref IS NULL OR length(task_ref) BETWEEN 1 AND 240),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    precedence_hash TEXT NOT NULL CHECK (precedence_hash ~ '^[0-9a-f]{64}$'),
    canonical_bundle_hash TEXT NOT NULL CHECK (canonical_bundle_hash ~ '^[0-9a-f]{64}$'),
    rendered_hash TEXT NOT NULL CHECK (rendered_hash ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK (status IN ('ready','invalidated')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    segment_count INTEGER NOT NULL CHECK (segment_count >= 0),
    included_segment_count INTEGER NOT NULL CHECK (included_segment_count >= 0),
    omitted_segment_count INTEGER NOT NULL CHECK (omitted_segment_count >= 0),
    total_bytes BIGINT NOT NULL CHECK (total_bytes >= 0),
    correlation_id TEXT CHECK (correlation_id IS NULL OR length(trim(correlation_id)) BETWEEN 1 AND 240),
    causation_id TEXT CHECK (causation_id IS NULL OR length(trim(causation_id)) BETWEEN 1 AND 240),
    created_at TIMESTAMPTZ NOT NULL,
    invalidated_at TIMESTAMPTZ,
    invalidation_reason TEXT CHECK (invalidation_reason IS NULL OR length(trim(invalidation_reason)) BETWEEN 1 AND 2000),
    UNIQUE (id, organization_id),
    UNIQUE (organization_id, idempotency_key),
    CONSTRAINT context_snapshots_organization_fk
        FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE RESTRICT,
    CONSTRAINT context_snapshots_actor_role_fk
        FOREIGN KEY (organization_id, actor_role_id)
        REFERENCES organization_roles(organization_id, id) ON DELETE RESTRICT,
    CHECK (included_segment_count + omitted_segment_count = segment_count),
    CHECK (
        (status = 'ready' AND invalidated_at IS NULL AND invalidation_reason IS NULL)
        OR
        (status = 'invalidated' AND invalidated_at IS NOT NULL AND invalidation_reason IS NOT NULL)
    ),
    CHECK (invalidated_at IS NULL OR invalidated_at >= created_at)
);

CREATE INDEX context_snapshots_actor_created_idx
    ON context_snapshots (organization_id, actor_role_id, created_at DESC, id DESC);
CREATE INDEX context_snapshots_status_created_idx
    ON context_snapshots (organization_id, status, created_at DESC, id DESC);

CREATE TABLE context_segments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snapshot_id BIGINT NOT NULL,
    organization_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    render_ordinal INTEGER NOT NULL CHECK (render_ordinal > 0),
    authority_priority INTEGER NOT NULL CHECK (authority_priority BETWEEN 0 AND 6),
    authority_tier TEXT NOT NULL CHECK (authority_tier IN (
        'immutable_safety','owner_decisions','canonical_registry_and_policies','organization_agent',
        'department_agent','role_profile','approved_memory','matched_approved_skills',
        'project_context','task_context','rag_evidence'
    )),
    source_kind TEXT NOT NULL CHECK (source_kind IN (
        'canonical_document','owner_constraint','organization_agent','department_agent','role_profile',
        'approved_memory','approved_skill','project_context','task_context','rag_evidence'
    )),
    source_reference TEXT NOT NULL CHECK (length(trim(source_reference)) BETWEEN 1 AND 500),
    source_version TEXT NOT NULL CHECK (length(trim(source_version)) BETWEEN 1 AND 240),
    instruction_class TEXT NOT NULL CHECK (instruction_class IN (
        'immutable_constraint','owner_constraint','canonical_policy','organizational_instruction',
        'role_instruction','scoped_instruction','approved_procedure','data'
    )),
    trust_class TEXT NOT NULL CHECK (trust_class IN ('immutable','authoritative','approved','scoped','untrusted')),
    data_class TEXT NOT NULL CHECK (data_class IN ('public','organizational','sanitized')),
    may_grant_capabilities BOOLEAN NOT NULL DEFAULT FALSE,
    included BOOLEAN NOT NULL,
    omission_reason TEXT CHECK (omission_reason IS NULL OR length(trim(omission_reason)) BETWEEN 1 AND 240),
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    byte_count INTEGER NOT NULL CHECK (byte_count >= 0 AND byte_count <= 1048576),
    content BYTEA CHECK (content IS NULL OR octet_length(content) <= 1048576),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (snapshot_id, ordinal),
    UNIQUE (snapshot_id, render_ordinal),
    CONSTRAINT context_segments_snapshot_fk
        FOREIGN KEY (snapshot_id, organization_id)
        REFERENCES context_snapshots(id, organization_id) ON DELETE RESTRICT,
    CHECK (
        (included AND content IS NOT NULL AND omission_reason IS NULL AND byte_count = octet_length(content))
        OR
        (NOT included AND content IS NULL AND omission_reason IS NOT NULL AND byte_count = 0)
    ),
    CHECK (NOT may_grant_capabilities OR authority_priority IN (1,2)),
    CHECK (authority_priority <> 0 OR NOT may_grant_capabilities),
    CHECK (
        (authority_tier = 'immutable_safety' AND authority_priority = 0)
        OR (authority_tier = 'owner_decisions' AND authority_priority = 1)
        OR (authority_tier = 'canonical_registry_and_policies' AND authority_priority = 2)
        OR (authority_tier IN ('organization_agent','department_agent') AND authority_priority = 3)
        OR (authority_tier IN ('role_profile','matched_approved_skills') AND authority_priority = 4)
        OR (authority_tier IN ('project_context','task_context') AND authority_priority = 5)
        OR (authority_tier IN ('approved_memory','rag_evidence') AND authority_priority = 6)
    ),
    CHECK (source_kind NOT IN ('approved_memory','rag_evidence','task_context') OR NOT may_grant_capabilities),
    CHECK (source_kind NOT IN ('approved_memory','rag_evidence') OR instruction_class = 'data'),
    CHECK (source_kind NOT IN ('approved_memory','rag_evidence') OR trust_class = 'untrusted')
);

CREATE INDEX context_segments_snapshot_render_idx
    ON context_segments (snapshot_id, render_ordinal);
CREATE INDEX context_segments_source_idx
    ON context_segments (organization_id, source_kind, source_reference);

CREATE FUNCTION reject_context_segment_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'context_segments are append-only' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER context_segments_no_update
BEFORE UPDATE OR DELETE ON context_segments
FOR EACH ROW EXECUTE FUNCTION reject_context_segment_mutation();
