CREATE TABLE shadow_verifier_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    mode TEXT NOT NULL CHECK (mode IN ('exhaustive','replay')),
    organization_revision_id BIGINT,
    capability_matrix_hash TEXT NOT NULL CHECK (capability_matrix_hash ~ '^[0-9a-f]{64}$'),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','failed')),
    checks_total INT NOT NULL DEFAULT 0 CHECK (checks_total >= 0),
    checks_parity INT NOT NULL DEFAULT 0 CHECK (checks_parity >= 0),
    checks_divergent INT NOT NULL DEFAULT 0 CHECK (checks_divergent >= 0),
    checks_counterexample INT NOT NULL DEFAULT 0 CHECK (checks_counterexample >= 0),
    checks_uncomparable INT NOT NULL DEFAULT 0 CHECK (checks_uncomparable >= 0),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX shadow_verifier_runs_organization_idx
    ON shadow_verifier_runs (organization_id, started_at DESC);

CREATE TABLE shadow_verifier_divergences (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES shadow_verifier_runs(id) ON DELETE CASCADE,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    fact TEXT NOT NULL CHECK (fact IN ('role_exists','department_exists','leader_of','may_message','may_delegate','capability_granted','dependency_closed')),
    kind TEXT NOT NULL CHECK (kind IN ('divergence','counterexample')),
    subject_role_id TEXT CHECK (subject_role_id IS NULL OR length(trim(subject_role_id)) BETWEEN 1 AND 320),
    subject_unit_id TEXT CHECK (subject_unit_id IS NULL OR length(trim(subject_unit_id)) BETWEEN 1 AND 160),
    capability_id TEXT CHECK (capability_id IS NULL OR length(trim(capability_id)) BETWEEN 1 AND 160),
    target_role_id TEXT CHECK (target_role_id IS NULL OR length(trim(target_role_id)) BETWEEN 1 AND 320),
    shadow_verdict TEXT NOT NULL CHECK (length(trim(shadow_verdict)) BETWEEN 1 AND 40),
    ground_verdict TEXT CHECK (ground_verdict IS NULL OR length(trim(ground_verdict)) BETWEEN 1 AND 40),
    detail TEXT NOT NULL CHECK (length(trim(detail)) BETWEEN 1 AND 2000),
    detected_at TIMESTAMPTZ NOT NULL,
    CHECK (kind <> 'divergence' OR ground_verdict IS NOT NULL)
);

CREATE INDEX shadow_verifier_divergences_run_idx
    ON shadow_verifier_divergences (run_id, id);

CREATE INDEX shadow_verifier_divergences_fact_idx
    ON shadow_verifier_divergences (fact, kind, detected_at DESC);
