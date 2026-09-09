-- Migration 000070: Dynamic Canonical Model Routing.
--
-- Adds a routing layer ABOVE the existing static role_model_bindings, for
-- policies that opt into routing_mode: pool in docs/canonical/model-routing.yaml.
-- Purely additive: role_model_bindings, model_profiles, model_profile_versions,
-- model_capability_snapshots, model_providers, model_invocations are UNCHANGED
-- in shape. Every existing static policy continues to resolve exactly as
-- before, through the unchanged role_model_bindings path.
--
-- A pool candidate is NOT a variant row bolted onto an existing table: each
-- candidate is materialized as its own ordinary model_profiles/
-- model_profile_versions/model_capability_snapshots/model_providers row set
-- (through the SAME BuildRegistryPlan/ApplyRegistry code that already
-- handles static policies), keyed by a synthetic per-candidate profile id.
-- routing_candidates only RECORDS which of those already-materialized,
-- already-FK-validated profile versions belong to which pool, in what
-- priority/capacity class. This is why no change to model_profile_versions'
-- "one row per (profile_id, version_number)" shape, or to its
-- versionIDs-keyed-by-profile-id materialization loop, was needed.
--
-- routing_policies.policy_id is the docs/canonical/model-routing.yaml
-- top-level policy key (e.g. "research.worker") -- the SAME identifier a
-- role's model_policy field names. A role bound to a pool policy has NO row
-- in role_model_bindings; InvocationService resolves it by looking up
-- routing_policies by (organization_id, organization_revision_id, policy_id)
-- instead, using the role's already-fetched model_policy.

CREATE TABLE routing_policies (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_revision_id BIGINT NOT NULL REFERENCES organization_registry_revisions(id) ON DELETE RESTRICT,
    policy_id TEXT NOT NULL,
    routing_mode TEXT NOT NULL CHECK (routing_mode IN ('static', 'pool')),
    selector_id TEXT,
    allow_paid BOOLEAN NOT NULL DEFAULT FALSE,
    canonical_hash TEXT NOT NULL CHECK (canonical_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, organization_revision_id, policy_id),
    -- A row only exists here for routing_mode='pool' (static policies are
    -- fully described by role_model_bindings and never get a row). The
    -- CHECK still pins the selector contract for the mode that needs it.
    CHECK (routing_mode <> 'pool' OR (selector_id IS NOT NULL AND length(trim(selector_id)) BETWEEN 1 AND 160)),
    CHECK (length(trim(policy_id)) BETWEEN 1 AND 160)
);

CREATE TABLE routing_candidates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    organization_revision_id BIGINT NOT NULL,
    policy_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    transport TEXT NOT NULL CHECK (transport IN ('cli_adapter', 'http_adapter', 'fake_adapter')),
    capacity_class TEXT NOT NULL CHECK (capacity_class IN ('free_daily', 'free_model', 'credit_monthly', 'paid')),
    priority INTEGER NOT NULL CHECK (priority >= 0),
    -- The candidate's own, independently materialized profile version --
    -- the actual FK-checked identity model_invocations will reference once
    -- this candidate is selected. NOT a shared/reused profile: one profile
    -- version per candidate, one candidate per profile version.
    profile_id TEXT NOT NULL,
    model_profile_version_id BIGINT NOT NULL,
    candidate_hash TEXT NOT NULL CHECK (candidate_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT routing_candidates_policy_fk
        FOREIGN KEY (organization_id, organization_revision_id, policy_id)
        REFERENCES routing_policies (organization_id, organization_revision_id, policy_id)
        ON DELETE RESTRICT,
    CONSTRAINT routing_candidates_version_fk
        FOREIGN KEY (model_profile_version_id, organization_id, profile_id, provider_id, provider_model_id)
        REFERENCES model_profile_versions (id, organization_id, profile_id, provider_id, provider_model_id)
        ON DELETE RESTRICT,
    -- No duplicate (provider, model) within one pool.
    UNIQUE (organization_id, organization_revision_id, policy_id, provider_id, provider_model_id),
    UNIQUE (model_profile_version_id),
    CHECK (length(trim(provider_model_id)) BETWEEN 1 AND 240)
);

CREATE INDEX routing_candidates_policy_idx
    ON routing_candidates (organization_id, organization_revision_id, policy_id);

-- Minimal audit/provenance on the invocation itself (Section 10): which
-- routing policy/selector produced this invocation's provider/model, and
-- the candidate-set digest that was in force. NULL for every invocation
-- created before this migration and for every static-routed invocation
-- going forward (routing_mode is recorded as 'static' explicitly so a
-- reader never has to infer "NULL means static" from absence).
ALTER TABLE model_invocations
    ADD COLUMN routing_mode TEXT CHECK (routing_mode IS NULL OR routing_mode IN ('static', 'pool')),
    ADD COLUMN routing_selector_id TEXT,
    ADD COLUMN routing_candidate_set_hash TEXT CHECK (routing_candidate_set_hash IS NULL OR routing_candidate_set_hash ~ '^[0-9a-f]{64}$');
