-- Migration 000072: durable model capacity state (Model Capacity State V1).
--
-- Purely additive on top of 000071 -- no column or table from any earlier
-- migration is altered, dropped, or renamed.
--
-- This table gives Dynamic Canonical Model Routing (kernel/dynamic-canonical-
-- model-routing, frozen at ea8f4baa6fca421f221562f1107611f94fbf5df8) a REAL,
-- durable, cross-invocation picture of candidate availability, read back
-- through modelruntime.CapacityStateReader and consumed by
-- internal/modelrouting's pure selectors (FreeCapacityV1). It does not
-- grant authority: it can only make an already-canonical candidate
-- (already present in routing_candidates) temporarily or administratively
-- ineligible. It can never create a provider/model, a candidate, or change
-- ProfileID/ModelProfileVersionID -- those remain exclusively
-- canonical YAML -> BuildRegistryPlan -> routing_candidates ->
-- RouteResolver -> ValidateResolvedRouteAgainstCanonical.
--
-- Deliberately keyed by (organization_id, provider_id, provider_model_id)
-- rather than a routing revision or routing_candidates row: capacity state
-- is mutable and outlives any one organization_registry_revisions row,
-- while the exact route a given Invocation took stays frozen forever on
-- model_invocations itself (model_profile_version_id/provider_id/
-- provider_model_id). NO foreign key to routing_candidates or to any
-- revision-scoped table (model_providers, model_profile_versions) is added
-- here on purpose -- see the architecture note above.
--
-- organization_id/last_provider_outcome_id/last_invocation_id/
-- last_dispatch_attempt_id name (never FK-reference) organizations/
-- model_provider_outcomes/model_invocations/model_dispatch_attempts --
-- deliberately plain TEXT/BIGINT, no foreign keys. The write path
-- (internal/modelruntime/postgres.insertProviderOutcome, the single caller
-- of applyCapacityFeedback) always derives all four from rows it just
-- inserted or looked up in the SAME transaction, so correctness is already
-- guaranteed by construction; a DB-level FK here would add no real
-- protection while permanently coupling this table's lifecycle to full
-- organization/outcome-history retention. last_provider_outcome_id doubles
-- as this projection's write-ordering version: the UPSERT only ever
-- advances state when the incoming outcome id is strictly greater than
-- what is already recorded, so a late-committing but logically older
-- outcome can never clobber a newer projection (concurrent Invocations
-- against the same candidate are expected).
--
-- disabled starts, and stays, false here: nothing in Model Capacity State
-- V1's automatic classifier (internal/modelruntime.ClassifyCapacityFeedback)
-- ever sets it. It is reserved for a future administrative/canonical
-- control surface -- included now only so the read side
-- (CapacityStateReader/modelrouting.CandidateState) already has a field for
-- it and nothing about this schema needs to change to wire that control in
-- later.
--
-- quota_exhausted is likewise never set automatically in V1: no adapter
-- today emits a closed, exact, cross-provider ErrorClass/ErrorCode token
-- that unambiguously means quota/credit exhaustion (see
-- internal/modelruntime/capacity_feedback.go's doc comment -- inventoried,
-- not assumed). The column, and the UPSERT path that can set it, exist so a
-- later explicitly-approved exact-token rule has somewhere durable to land
-- without another migration.
--
-- consecutive_capacity_failures is a narrow, concrete convenience: it is
-- what lets a healthy response deterministically prove "the degraded state
-- is over" (reset to 0) versus a cooldown deterministically prove "another
-- one just happened" (increment) in the same UPSERT, without a second
-- read-modify-write round trip.
CREATE TABLE model_routing_capacity_state (
    organization_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,

    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    quota_exhausted BOOLEAN NOT NULL DEFAULT FALSE,
    cooldown_until TIMESTAMPTZ,
    consecutive_capacity_failures INTEGER NOT NULL DEFAULT 0,

    last_provider_outcome_id BIGINT,
    last_invocation_id BIGINT,
    last_dispatch_attempt_id BIGINT,

    last_outcome_classification TEXT,
    last_http_status INTEGER CHECK (last_http_status IS NULL OR last_http_status BETWEEN 100 AND 599),
    last_error_class TEXT,
    last_error_code TEXT,
    last_retryable BOOLEAN,
    last_observed_at TIMESTAMPTZ,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

    PRIMARY KEY (organization_id, provider_id, provider_model_id),
    CHECK (length(trim(organization_id)) BETWEEN 1 AND 160),
    CHECK (length(trim(provider_id)) BETWEEN 1 AND 160),
    CHECK (length(trim(provider_model_id)) BETWEEN 1 AND 240),
    CHECK (consecutive_capacity_failures >= 0),
    CHECK (last_outcome_classification IS NULL OR length(trim(last_outcome_classification)) BETWEEN 1 AND 160),
    CHECK (last_error_class IS NULL OR length(trim(last_error_class)) BETWEEN 1 AND 120),
    CHECK (last_error_code IS NULL OR length(trim(last_error_code)) BETWEEN 1 AND 160)
);

-- Supports a future reconciliation sweep ("which candidates are currently
-- cooling down") without a sequential scan. Not used by
-- CapacityStateReader itself, which always looks up one exact
-- (organization_id, provider_id, provider_model_id) via the primary key.
CREATE INDEX model_routing_capacity_state_cooldown_idx
    ON model_routing_capacity_state (cooldown_until)
    WHERE cooldown_until IS NOT NULL;
