-- Migration 000067: Create SkillForge and provider divergence tables

CREATE TABLE skill_provider_divergences (
    id BIGSERIAL PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    divergence_key TEXT NOT NULL CHECK (divergence_key ~ '^[0-9a-f]{64}$'),
    role_id TEXT NOT NULL CHECK (length(trim(role_id)) BETWEEN 1 AND 240),
    skill_id TEXT,
    operation TEXT NOT NULL CHECK (length(trim(operation)) BETWEEN 1 AND 100),
    field TEXT,
    primary_value TEXT,
    shadow_value TEXT,
    primary_version TEXT,
    shadow_version TEXT,
    primary_source_hash TEXT,
    shadow_source_hash TEXT,
    reason TEXT NOT NULL,
    first_observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    observation_count BIGINT NOT NULL DEFAULT 1 CHECK (observation_count >= 1),

    UNIQUE (organization_id, divergence_key),
    CHECK (last_observed_at >= first_observed_at)
);

CREATE INDEX skill_provider_divergences_lookup_idx
    ON skill_provider_divergences (organization_id, role_id, last_observed_at DESC);

CREATE TABLE skillforge_procedure_needs (
    id TEXT NOT NULL CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    role_id TEXT NOT NULL CHECK (length(trim(role_id)) BETWEEN 1 AND 240),
    task_class TEXT,
    execution_profile_id TEXT,
    problem_statement TEXT NOT NULL CHECK (length(trim(problem_statement)) >= 10),
    episode_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(episode_refs) = 'array'),
    cluster_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(cluster_refs) = 'array'),
    evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    suggested_skill_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(suggested_skill_ids) = 'array'),
    status TEXT NOT NULL CHECK (status IN ('open', 'accepted', 'authored', 'rejected', 'superseded')),
    acceptance JSONB,
    canonical_digest TEXT NOT NULL CHECK (canonical_digest ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (organization_id, id, revision),
    CHECK (updated_at >= created_at)
);

CREATE INDEX skillforge_procedure_needs_status_idx
    ON skillforge_procedure_needs (organization_id, status, created_at DESC);

CREATE TABLE skill_source_materializations (
    id TEXT NOT NULL CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    skill_id TEXT NOT NULL CHECK (length(trim(skill_id)) BETWEEN 1 AND 100),
    origin_ref TEXT NOT NULL CHECK (origin_ref ~ '^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+@[0-9a-f]{40}$'),
    path TEXT NOT NULL CHECK (path LIKE '%SKILL.md'),
    raw_sha256 TEXT NOT NULL CHECK (raw_sha256 ~ '^[0-9a-f]{64}$'),
    normalized_sha256 TEXT NOT NULL CHECK (normalized_sha256 ~ '^[0-9a-f]{64}$'),
    recorded_by TEXT NOT NULL CHECK (length(trim(recorded_by)) BETWEEN 1 AND 240),
    record_ref TEXT NOT NULL CHECK (length(trim(record_ref)) BETWEEN 1 AND 240),
    materialized_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (organization_id, id)
);

CREATE INDEX skill_source_materializations_skill_idx
    ON skill_source_materializations (organization_id, skill_id, materialized_at DESC);

CREATE TABLE skillforge_runs (
    id TEXT NOT NULL CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    need_id TEXT NOT NULL CHECK (length(trim(need_id)) BETWEEN 1 AND 200),
    revision BIGINT NOT NULL CHECK (revision > 0),
    status TEXT NOT NULL CHECK (status IN ('created', 'searching', 'authoring', 'publishing', 'materializing', 'draft_registered', 'waiting_human_approval', 'validating', 'evaluating', 'adversarial_review', 'canary', 'candidate_ready', 'rejected', 'failed')),
    current_step TEXT NOT NULL CHECK (length(trim(current_step)) BETWEEN 1 AND 100),
    input_digest TEXT NOT NULL CHECK (input_digest ~ '^[0-9a-f]{64}$'),
    skill_version_id TEXT,
    search_record JSONB,
    author_record JSONB,
    published_source JSONB,
    materialized_source JSONB,
    validation_result JSONB,
    evaluation_result JSONB,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,

    PRIMARY KEY (organization_id, id, revision),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX skillforge_runs_need_idx
    ON skillforge_runs (organization_id, need_id, status);

CREATE TABLE skillforge_events (
    run_id TEXT NOT NULL CHECK (length(trim(run_id)) BETWEEN 1 AND 200),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) BETWEEN 1 AND 100),
    refs JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(refs) = 'object'),
    digest TEXT,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (run_id, sequence)
);

CREATE INDEX skillforge_events_lookup_idx
    ON skillforge_events (organization_id, event_type, recorded_at DESC);

CREATE TABLE skillforge_evaluations (
    id TEXT NOT NULL CHECK (length(trim(id)) BETWEEN 1 AND 200),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    run_id TEXT NOT NULL CHECK (length(trim(run_id)) BETWEEN 1 AND 200),
    candidate_version_id TEXT NOT NULL CHECK (length(trim(candidate_version_id)) BETWEEN 1 AND 200),
    candidate_canonical_hash TEXT NOT NULL CHECK (candidate_canonical_hash ~ '^[0-9a-f]{64}$'),
    candidate_source_hash TEXT NOT NULL CHECK (candidate_source_hash ~ '^[0-9a-f]{64}$'),
    baseline_version_id TEXT,
    suite_ref TEXT NOT NULL CHECK (length(trim(suite_ref)) BETWEEN 1 AND 240),
    metrics JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metrics) = 'object'),
    adversarial_verdict TEXT NOT NULL CHECK (adversarial_verdict IN ('pass', 'fail', 'skipped')),
    canary_verdict TEXT NOT NULL CHECK (canary_verdict IN ('pass', 'fail', 'skipped')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (organization_id, id)
);

CREATE INDEX skillforge_evaluations_run_idx
    ON skillforge_evaluations (organization_id, run_id);
