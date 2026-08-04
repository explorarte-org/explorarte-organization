CREATE TABLE organization_registry_revisions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    previous_revision_id BIGINT REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('applied')),
    schema_versions JSONB NOT NULL DEFAULT '{}'::jsonb,
    document_hashes JSONB NOT NULL DEFAULT '{}'::jsonb,
    counts JSONB NOT NULL DEFAULT '{}'::jsonb,
    diff JSONB NOT NULL DEFAULT '{}'::jsonb,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX organization_registry_revisions_hash_idx
    ON organization_registry_revisions (canonical_hash);

CREATE INDEX organization_registry_revisions_applied_at_idx
    ON organization_registry_revisions (applied_at DESC);

CREATE TABLE organization_registry_revision_documents (
    revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    document_path TEXT NOT NULL,
    schema_version TEXT,
    document_status TEXT,
    semantic_hash TEXT NOT NULL CHECK (semantic_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (revision_id, document_path),
    CHECK (length(trim(document_path)) > 0)
);

CREATE TABLE organizations (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    owner_role_id TEXT NOT NULL,
    ceo_role_id TEXT NOT NULL,
    runtime_target TEXT NOT NULL,
    state_store_target TEXT NOT NULL,
    cells_are_external BOOLEAN NOT NULL,
    schema_version TEXT NOT NULL,
    document_status TEXT NOT NULL,
    current_revision_id BIGINT REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at TIMESTAMPTZ,
    CHECK (id ~ '^[a-z0-9]+([-_][a-z0-9]+)*$'),
    CHECK (length(trim(display_name)) > 0),
    CHECK (owner_role_id ~ '^[a-z0-9]+([-_][a-z0-9]+)*/[a-z0-9]+([-_][a-z0-9]+)*$'),
    CHECK (ceo_role_id ~ '^[a-z0-9]+([-_][a-z0-9]+)*/[a-z0-9]+([-_][a-z0-9]+)*$')
);

CREATE TABLE organizational_units (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    operational BOOLEAN NOT NULL,
    leaderless BOOLEAN NOT NULL,
    leader_role_id TEXT,
    agent_path TEXT,
    schedule TEXT,
    timezone TEXT,
    canonical_status TEXT NOT NULL,
    source_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at TIMESTAMPTZ,
    PRIMARY KEY (organization_id, id),
    CHECK (id ~ '^[a-z0-9]+([-_][a-z0-9]+)*$'),
    CHECK (length(trim(display_name)) > 0),
    CHECK (length(trim(kind)) > 0),
    CHECK ((leaderless AND leader_role_id IS NULL) OR (NOT leaderless AND leader_role_id IS NOT NULL)),
    CHECK (NOT operational OR NOT leaderless)
);

CREATE TABLE organization_roles (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    id TEXT NOT NULL,
    unit_id TEXT NOT NULL,
    role_slug TEXT NOT NULL,
    display_name TEXT NOT NULL,
    runtime_kind TEXT NOT NULL,
    authority_class TEXT NOT NULL,
    canonical_leader BOOLEAN NOT NULL,
    legacy_frontmatter_leader BOOLEAN NOT NULL,
    model_policy TEXT,
    profile_path TEXT,
    department_agent_path TEXT,
    memory_domain TEXT,
    memory_path TEXT,
    summary TEXT,
    reports_to_source_text TEXT,
    rag_topics_source_text TEXT,
    source_status TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    executable BOOLEAN NOT NULL,
    source_hash TEXT NOT NULL CHECK (source_hash ~ '^[0-9a-f]{64}$'),
    source_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at TIMESTAMPTZ,
    PRIMARY KEY (organization_id, id),
    CONSTRAINT organization_roles_unit_fk
        FOREIGN KEY (organization_id, unit_id)
        REFERENCES organizational_units(organization_id, id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    CHECK (id ~ '^[a-z0-9]+([-_][a-z0-9]+)*/[a-z0-9]+([-_][a-z0-9]+)*$'),
    CHECK (role_slug ~ '^[a-z0-9]+([-_][a-z0-9]+)*$'),
    CHECK (id = unit_id || '/' || role_slug),
    CHECK (length(trim(display_name)) > 0),
    CHECK (length(trim(runtime_kind)) > 0),
    CHECK (length(trim(authority_class)) > 0),
    CHECK (source_status IN ('imported_source', 'proposed_profile_required')),
    CHECK (source_status <> 'proposed_profile_required' OR (NOT enabled AND NOT executable)),
    CHECK (NOT executable OR enabled)
);

ALTER TABLE organizations
    ADD CONSTRAINT organizations_owner_role_fk
    FOREIGN KEY (id, owner_role_id)
    REFERENCES organization_roles(organization_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE organizations
    ADD CONSTRAINT organizations_ceo_role_fk
    FOREIGN KEY (id, ceo_role_id)
    REFERENCES organization_roles(organization_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE organizational_units
    ADD CONSTRAINT organizational_units_leader_role_fk
    FOREIGN KEY (organization_id, leader_role_id)
    REFERENCES organization_roles(organization_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE organization_reporting_lines (
    revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    role_id TEXT NOT NULL,
    reports_to_role_id TEXT NOT NULL,
    relationship TEXT NOT NULL DEFAULT 'reports_to',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (revision_id, organization_id, role_id, reports_to_role_id, relationship),
    CONSTRAINT organization_reporting_lines_role_fk
        FOREIGN KEY (organization_id, role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT organization_reporting_lines_target_fk
        FOREIGN KEY (organization_id, reports_to_role_id)
        REFERENCES organization_roles(organization_id, id)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED,
    CHECK (role_id <> reports_to_role_id),
    CHECK (length(trim(relationship)) > 0)
);

CREATE INDEX organizational_units_kind_status_idx
    ON organizational_units (organization_id, operational, canonical_status)
    WHERE retired_at IS NULL;

CREATE INDEX organizational_units_leader_idx
    ON organizational_units (organization_id, leader_role_id)
    WHERE retired_at IS NULL AND leader_role_id IS NOT NULL;

CREATE INDEX organization_roles_unit_idx
    ON organization_roles (organization_id, unit_id, id)
    WHERE retired_at IS NULL;

CREATE INDEX organization_roles_leader_idx
    ON organization_roles (organization_id, unit_id, canonical_leader)
    WHERE retired_at IS NULL;

CREATE INDEX organization_roles_status_idx
    ON organization_roles (organization_id, source_status, enabled)
    WHERE retired_at IS NULL;

CREATE INDEX organization_roles_model_policy_idx
    ON organization_roles (model_policy)
    WHERE retired_at IS NULL AND model_policy IS NOT NULL;

CREATE INDEX organization_reporting_lines_role_idx
    ON organization_reporting_lines (organization_id, role_id, revision_id DESC);

CREATE INDEX organization_reporting_lines_target_idx
    ON organization_reporting_lines (organization_id, reports_to_role_id, revision_id DESC);
