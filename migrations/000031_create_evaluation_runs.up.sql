-- R30 phase 3: durable storage for canary evaluation runs. Each run is one
-- execution of fixtures.RunSuite (internal/evaluation/fixtures) against one
-- subject (a retrieval mode such as "lexical"/"gemini-hybrid"/
-- "bge-m3-hybrid", or another organizational configuration under test).
-- Outcomes are append-only, one row per (run, fixture) — a rerun of the
-- same fixture under the same subject is a NEW run, never an update to an
-- existing outcome row, so a comparison between two runs can never be
-- confused by an outcome silently changing under it mid-comparison.
CREATE TABLE evaluation_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    suite_id TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_by TEXT NOT NULL
);

CREATE INDEX evaluation_runs_org_suite_idx ON evaluation_runs (organization_id, suite_id, started_at DESC);

CREATE TABLE evaluation_run_outcomes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES evaluation_runs (id) ON DELETE CASCADE,
    fixture_id TEXT NOT NULL,
    passed BOOLEAN NOT NULL,
    invariant_results JSONB NOT NULL,
    violated_invariants JSONB NOT NULL,
    evidence_refs JSONB NOT NULL,
    metrics JSONB NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, fixture_id)
);

CREATE INDEX evaluation_run_outcomes_run_idx ON evaluation_run_outcomes (run_id);
