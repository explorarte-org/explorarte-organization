-- MemoryOS Phase 1: durable clusters for recurring corrective patterns.
-- Clusters aggregate historical obligation-level observations across distinct
-- Harness runs; they remain metadata-only facts and never grant memory authority.

CREATE TABLE memoryos_clusters (
    id TEXT NOT NULL CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    canonical_digest TEXT NOT NULL CHECK (canonical_digest ~ '^[0-9a-f]{64}$'),
    cluster_kind TEXT NOT NULL CHECK (cluster_kind IN ('corrective')),
    role_id TEXT NOT NULL CHECK (length(trim(role_id)) BETWEEN 1 AND 240),
    task_class TEXT NOT NULL CHECK (length(trim(task_class)) BETWEEN 1 AND 100),
    execution_profile_id TEXT NOT NULL CHECK (length(trim(execution_profile_id)) BETWEEN 1 AND 240),
    obligation_key TEXT NOT NULL CHECK (length(trim(obligation_key)) BETWEEN 1 AND 240),
    obligation_kind TEXT NOT NULL CHECK (length(trim(obligation_kind)) BETWEEN 1 AND 120),
    episode_ids JSONB NOT NULL CHECK (jsonb_typeof(episode_ids) = 'array'),
    decision_run_refs JSONB NOT NULL CHECK (jsonb_typeof(decision_run_refs) = 'array'),
    pass_count INTEGER NOT NULL CHECK (pass_count >= 0),
    fail_count INTEGER NOT NULL CHECK (fail_count >= 0),
    first_observed TIMESTAMPTZ NOT NULL,
    last_observed TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('observed', 'candidate_emitted', 'superseded')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, revision),
    UNIQUE (organization_id, id, revision),
    UNIQUE (organization_id, id, canonical_digest, status),
    CHECK (last_observed >= first_observed)
);

CREATE INDEX memoryos_clusters_lookup_idx
    ON memoryos_clusters (organization_id, cluster_kind, role_id, task_class, obligation_key, obligation_kind, last_observed DESC);

CREATE TRIGGER memoryos_clusters_immutable
    BEFORE UPDATE OR DELETE ON memoryos_clusters
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
