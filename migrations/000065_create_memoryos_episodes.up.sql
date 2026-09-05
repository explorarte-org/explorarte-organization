-- MemoryOS Phase 1: one metadata-only, immutable materialized projection per
-- Harness run. Context/prompt/result bodies are deliberately absent; their
-- owners remain context-engine, model-runtime and the Harness history ledger.

CREATE TABLE memoryos_episodes (
    id TEXT NOT NULL CHECK (id ~ '^[0-9a-f]{64}$'),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    harness_run_id TEXT NOT NULL CHECK (length(trim(harness_run_id)) BETWEEN 1 AND 200),
    revision BIGINT NOT NULL CHECK (revision > 0),
    canonical_digest TEXT NOT NULL CHECK (canonical_digest ~ '^[0-9a-f]{64}$'),
    task_id BIGINT NOT NULL,
    attempt_id BIGINT NOT NULL,
    decision_run_id BIGINT,
    role_id TEXT NOT NULL CHECK (length(trim(role_id)) BETWEEN 1 AND 240),
    execution_principal_id TEXT NOT NULL CHECK (length(trim(execution_principal_id)) BETWEEN 1 AND 240),
    task_class TEXT NOT NULL CHECK (length(trim(task_class)) BETWEEN 1 AND 100),
    execution_purpose TEXT NOT NULL CHECK (length(trim(execution_purpose)) BETWEEN 1 AND 120),
    execution_profile_id TEXT NOT NULL CHECK (length(trim(execution_profile_id)) BETWEEN 1 AND 240),
    context_snapshot_id BIGINT NOT NULL,
    context_snapshot_version TEXT NOT NULL CHECK (length(trim(context_snapshot_version)) BETWEEN 1 AND 240),
    context_snapshot_digest TEXT NOT NULL CHECK (context_snapshot_digest ~ '^[0-9a-f]{64}$'),
    context_provider_visible_digest TEXT NOT NULL CHECK (context_provider_visible_digest ~ '^[0-9a-f]{64}$'),
    execution_context_view_id BIGINT,
    binding_mode TEXT NOT NULL CHECK (binding_mode IN ('homogeneous','mixed')),
    turns_used INTEGER NOT NULL CHECK (turns_used >= 0),
    tool_calls_used INTEGER NOT NULL CHECK (tool_calls_used >= 0),
    actual_cost_usd_nanos BIGINT CHECK (actual_cost_usd_nanos IS NULL OR actual_cost_usd_nanos >= 0),
    estimated_cost_usd_nanos BIGINT CHECK (estimated_cost_usd_nanos IS NULL OR estimated_cost_usd_nanos >= 0),
    terminal_status TEXT NOT NULL DEFAULT '' CHECK (length(terminal_status) <= 120),
    status TEXT NOT NULL CHECK (status IN ('observed','incomplete')),
    event_count INTEGER NOT NULL CHECK (event_count >= 0),
    source_facts_digest TEXT NOT NULL CHECK (source_facts_digest ~ '^[0-9a-f]{64}$'),
    incomplete_reasons JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(incomplete_reasons) = 'array'),
    verification_verdict TEXT CHECK (verification_verdict IS NULL OR verification_verdict IN ('pass','fail','inconclusive')),
    verification_scope TEXT CHECK (verification_scope IS NULL OR verification_scope IN ('task_attempt')),
    verification_verified_at TIMESTAMPTZ,
    verification_decision_run_id BIGINT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A task/attempt pair can contain several independent Harness runs. The
    -- run identifier, rather than task or attempt, is the Episode grain.
    PRIMARY KEY (id, revision),
    UNIQUE (organization_id, harness_run_id, canonical_digest),
    UNIQUE (organization_id, harness_run_id, revision),
    UNIQUE (id, revision, organization_id),
    CONSTRAINT memoryos_episodes_task_org_fk
        FOREIGN KEY (task_id, organization_id)
        REFERENCES tasks(id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_episodes_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_episodes_context_org_fk
        FOREIGN KEY (context_snapshot_id, organization_id)
        REFERENCES context_snapshots(id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_episodes_view_fk
        FOREIGN KEY (execution_context_view_id)
        REFERENCES execution_context_views(id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_episodes_decision_org_fk
        FOREIGN KEY (decision_run_id, organization_id)
        REFERENCES decision_graph_runs(id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_episodes_verification_decision_org_fk
        FOREIGN KEY (verification_decision_run_id, organization_id)
        REFERENCES decision_graph_runs(id, organization_id) ON DELETE RESTRICT,
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at),
    CHECK ((verification_verdict IS NULL) = (verification_verified_at IS NULL)),
    CHECK ((verification_verdict IS NULL) = (verification_scope IS NULL)),
    CHECK ((verification_decision_run_id IS NULL) OR verification_verdict IS NOT NULL)
);

CREATE INDEX memoryos_episodes_org_created_idx
    ON memoryos_episodes (organization_id, created_at DESC, id);
CREATE INDEX memoryos_episodes_org_task_attempt_idx
    ON memoryos_episodes (organization_id, task_id, attempt_id, id);
CREATE INDEX memoryos_episodes_org_terminal_idx
    ON memoryos_episodes (organization_id, terminal_status, id);

CREATE TABLE memoryos_episode_skills (
    episode_id TEXT NOT NULL,
    episode_revision BIGINT NOT NULL CHECK (episode_revision > 0),
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    skill_id TEXT NOT NULL CHECK (length(trim(skill_id)) BETWEEN 1 AND 500),
    version TEXT NOT NULL CHECK (length(trim(version)) BETWEEN 1 AND 240),
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    available BOOLEAN,
    requested BOOLEAN,
    resolved BOOLEAN,
    included BOOLEAN NOT NULL,
    PRIMARY KEY (episode_id, episode_revision, ordinal),
    UNIQUE (episode_id, episode_revision, skill_id, version, content_hash),
    CONSTRAINT memoryos_episode_skills_episode_fk
        FOREIGN KEY (episode_id, episode_revision)
        REFERENCES memoryos_episodes(id, revision) ON DELETE RESTRICT
);

CREATE TABLE memoryos_episode_invocations (
    episode_id TEXT NOT NULL,
    episode_revision BIGINT NOT NULL CHECK (episode_revision > 0),
    organization_id TEXT NOT NULL,
    invocation_id BIGINT NOT NULL,
    provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) BETWEEN 1 AND 160),
    provider_model_id TEXT NOT NULL CHECK (length(trim(provider_model_id)) BETWEEN 1 AND 240),
    input_tokens BIGINT CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens IS NULL OR output_tokens >= 0),
    reasoning_tokens BIGINT CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0),
    cost_usd_nanos BIGINT CHECK (cost_usd_nanos IS NULL OR cost_usd_nanos >= 0),
    estimated_usd_nanos BIGINT CHECK (estimated_usd_nanos IS NULL OR estimated_usd_nanos >= 0),
    status TEXT NOT NULL CHECK (length(trim(status)) BETWEEN 1 AND 120),
    created_at TIMESTAMPTZ,
    terminal_at TIMESTAMPTZ,
    PRIMARY KEY (episode_id, episode_revision, invocation_id),
    CONSTRAINT memoryos_episode_invocation_episode_org_fk
        FOREIGN KEY (episode_id, episode_revision, organization_id)
        REFERENCES memoryos_episodes(id, revision, organization_id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_episode_invocation_org_fk
        FOREIGN KEY (invocation_id, organization_id)
        REFERENCES model_invocations(id, organization_id) ON DELETE RESTRICT
);

CREATE TABLE memoryos_episode_tools (
    episode_id TEXT NOT NULL,
    episode_revision BIGINT NOT NULL CHECK (episode_revision > 0),
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    tool_call_id TEXT NOT NULL CHECK (length(tool_call_id) <= 200),
    tool_name TEXT NOT NULL CHECK (length(trim(tool_name)) BETWEEN 1 AND 160),
    definition_digest TEXT NOT NULL DEFAULT '' CHECK (definition_digest = '' OR definition_digest ~ '^[0-9a-f]{64}$'),
    outcome TEXT NOT NULL CHECK (outcome IN ('requested','denied','ok','error','indeterminate')),
    latency_ms BIGINT CHECK (latency_ms IS NULL OR latency_ms >= 0),
    provenance TEXT NOT NULL DEFAULT '' CHECK (length(provenance) <= 500),
    PRIMARY KEY (episode_id, episode_revision, ordinal),
    UNIQUE (episode_id, episode_revision, tool_call_id),
    CONSTRAINT memoryos_episode_tools_episode_fk
        FOREIGN KEY (episode_id, episode_revision)
        REFERENCES memoryos_episodes(id, revision) ON DELETE RESTRICT
);

CREATE TABLE memoryos_episode_obligations (
    episode_id TEXT NOT NULL,
    episode_revision BIGINT NOT NULL CHECK (episode_revision > 0),
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    obligation_key TEXT NOT NULL CHECK (length(trim(obligation_key)) BETWEEN 1 AND 240),
    obligation_kind TEXT NOT NULL CHECK (length(trim(obligation_kind)) BETWEEN 1 AND 120),
    label TEXT NOT NULL CHECK (label IN ('verified','inferred','unknown','contradicted')),
    verifier_ref TEXT NOT NULL CHECK (length(trim(verifier_ref)) BETWEEN 1 AND 240),
    verifier_version TEXT NOT NULL CHECK (length(trim(verifier_version)) BETWEEN 1 AND 120),
    evidence_digest TEXT CHECK (evidence_digest IS NULL OR evidence_digest ~ '^[0-9a-f]{64}$'),
    evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    PRIMARY KEY (episode_id, episode_revision, ordinal),
    UNIQUE (episode_id, episode_revision, obligation_key, obligation_kind),
    CONSTRAINT memoryos_episode_obligations_episode_fk
        FOREIGN KEY (episode_id, episode_revision)
        REFERENCES memoryos_episodes(id, revision) ON DELETE RESTRICT
);

-- Host-owned completion observations retain only the obligation-level facts
-- that completion actually verified. They are immutable and can be reused by
-- projection after a restart; they never grant completion or memory authority.
CREATE TABLE memoryos_completion_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL,
    attempt_id BIGINT NOT NULL,
    observation_digest TEXT NOT NULL CHECK (observation_digest ~ '^[0-9a-f]{64}$'),
    verdict TEXT NOT NULL CHECK (verdict IN ('pass','fail','inconclusive')),
    verified_at TIMESTAMPTZ NOT NULL,
    obligations JSONB NOT NULL CHECK (jsonb_typeof(obligations) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, task_id, attempt_id, observation_digest),
    CONSTRAINT memoryos_completion_task_org_fk
        FOREIGN KEY (task_id, organization_id)
        REFERENCES tasks(id, organization_id) ON DELETE RESTRICT,
    CONSTRAINT memoryos_completion_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id) ON DELETE RESTRICT
);

CREATE INDEX memoryos_completion_observations_lookup_idx
    ON memoryos_completion_observations (organization_id, task_id, attempt_id, verified_at DESC, id DESC);

CREATE OR REPLACE FUNCTION memoryos_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'MemoryOS materialized facts are append-only';
END;
$$;

CREATE TRIGGER memoryos_episodes_immutable
    BEFORE UPDATE OR DELETE ON memoryos_episodes
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
CREATE TRIGGER memoryos_episode_skills_immutable
    BEFORE UPDATE OR DELETE ON memoryos_episode_skills
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
CREATE TRIGGER memoryos_episode_invocations_immutable
    BEFORE UPDATE OR DELETE ON memoryos_episode_invocations
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
CREATE TRIGGER memoryos_episode_tools_immutable
    BEFORE UPDATE OR DELETE ON memoryos_episode_tools
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
CREATE TRIGGER memoryos_episode_obligations_immutable
    BEFORE UPDATE OR DELETE ON memoryos_episode_obligations
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
CREATE TRIGGER memoryos_completion_observations_immutable
    BEFORE UPDATE OR DELETE ON memoryos_completion_observations
    FOR EACH ROW EXECUTE FUNCTION memoryos_reject_mutation();
