CREATE TABLE decision_graph_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('planned','running','waiting','succeeded','failed','cancelled','ambiguous')),
    reasoning_policy_schema_version TEXT NOT NULL,
    reasoning_policy_hash TEXT NOT NULL CHECK (reasoning_policy_hash ~ '^[0-9a-f]{64}$'),
    idempotency_key TEXT NOT NULL,
    max_nodes BIGINT NOT NULL CHECK (max_nodes > 0),
    max_depth BIGINT NOT NULL CHECK (max_depth > 0),
    max_parallel_nodes BIGINT NOT NULL CHECK (max_parallel_nodes > 0),
    max_model_calls BIGINT NOT NULL CHECK (max_model_calls > 0),
    max_input_tokens BIGINT NOT NULL CHECK (max_input_tokens > 0),
    max_output_tokens BIGINT NOT NULL CHECK (max_output_tokens > 0),
    max_replans BIGINT NOT NULL CHECK (max_replans > 0),
    max_verifications BIGINT NOT NULL CHECK (max_verifications > 0),
    max_wall_time_ms BIGINT NOT NULL CHECK (max_wall_time_ms > 0),
    deadline TIMESTAMPTZ NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_at TIMESTAMPTZ,
    terminal_reason_code TEXT,
    CONSTRAINT decision_graph_runs_attempt_task_fk
        FOREIGN KEY (attempt_id, task_id)
        REFERENCES task_attempts(id, task_id)
        ON DELETE RESTRICT,
    UNIQUE (organization_id, idempotency_key),
    UNIQUE (id, organization_id),
    CHECK (length(trim(reasoning_policy_schema_version)) BETWEEN 1 AND 120),
    CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 200),
    CHECK (length(trim(created_by)) BETWEEN 1 AND 200),
    CHECK (deadline > created_at),
    CHECK (updated_at >= created_at),
    CHECK (terminal_at IS NULL OR terminal_at >= created_at),
    CHECK (terminal_reason_code IS NULL OR length(trim(terminal_reason_code)) BETWEEN 1 AND 120),
    CHECK ((status IN ('succeeded','failed','cancelled','ambiguous')) = (terminal_at IS NOT NULL))
);

CREATE INDEX decision_graph_runs_active_idx
    ON decision_graph_runs (organization_id, status, deadline, id)
    WHERE status IN ('planned','running','waiting');
CREATE INDEX decision_graph_runs_task_idx
    ON decision_graph_runs (task_id, attempt_id, id);

CREATE TABLE decision_graph_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    snapshot_hash TEXT NOT NULL CHECK (snapshot_hash ~ '^[0-9a-f]{64}$'),
    node_count INTEGER NOT NULL CHECK (node_count > 0),
    max_depth INTEGER NOT NULL CHECK (max_depth >= 0),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_graph_versions_run_fk
        FOREIGN KEY (run_id, organization_id)
        REFERENCES decision_graph_runs(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (run_id, version_number),
    UNIQUE (run_id, snapshot_hash),
    UNIQUE (id, run_id, organization_id),
    CHECK (length(trim(created_by)) BETWEEN 1 AND 200)
);

CREATE TABLE decision_graph_nodes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    graph_version_id BIGINT NOT NULL,
    logical_node_id BIGINT NOT NULL CHECK (logical_node_id > 0),
    node_type TEXT NOT NULL CHECK (node_type IN ('goal','requirement','constraint','hypothesis','candidate_action','evidence','verification','decision')),
    branch_state TEXT NOT NULL CHECK (branch_state IN ('active','selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')),
    execution_state TEXT NOT NULL CHECK (execution_state IN ('pending','ready','running','waiting_verification','succeeded','failed','cancelled','ambiguous')),
    payload_schema_version TEXT NOT NULL,
    payload_hash TEXT NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    context_snapshot_id BIGINT REFERENCES context_snapshots(id) ON DELETE RESTRICT,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_at TIMESTAMPTZ,
    CONSTRAINT decision_graph_nodes_version_fk
        FOREIGN KEY (graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_versions(id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (graph_version_id, logical_node_id),
    UNIQUE (id, graph_version_id, run_id, organization_id),
    UNIQUE (id, run_id, organization_id),
    CHECK (length(trim(payload_schema_version)) BETWEEN 1 AND 120),
    CHECK (length(trim(created_by)) BETWEEN 1 AND 200),
    CHECK (updated_at >= created_at),
    CHECK (terminal_at IS NULL OR terminal_at >= created_at),
    CHECK ((execution_state IN ('succeeded','failed','cancelled','ambiguous')) = (terminal_at IS NOT NULL))
);

CREATE UNIQUE INDEX decision_graph_nodes_one_goal_idx
    ON decision_graph_nodes (graph_version_id)
    WHERE node_type = 'goal';
CREATE INDEX decision_graph_nodes_ready_idx
    ON decision_graph_nodes (run_id, graph_version_id, execution_state, id)
    WHERE branch_state = 'active' AND execution_state IN ('pending','ready');

CREATE TABLE decision_graph_edges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    graph_version_id BIGINT NOT NULL,
    from_node_id BIGINT NOT NULL,
    to_node_id BIGINT NOT NULL,
    edge_type TEXT NOT NULL CHECK (edge_type IN ('depends_on','supports','contradicts','satisfies','prunes','selected_from')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_graph_edges_from_fk
        FOREIGN KEY (from_node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT decision_graph_edges_to_fk
        FOREIGN KEY (to_node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (graph_version_id, from_node_id, to_node_id, edge_type),
    CHECK (from_node_id <> to_node_id)
);

CREATE INDEX decision_graph_edges_dependency_idx
    ON decision_graph_edges (graph_version_id, from_node_id, to_node_id)
    WHERE edge_type = 'depends_on';

CREATE TABLE decision_branch_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    graph_version_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    from_branch_state TEXT NOT NULL CHECK (from_branch_state IN ('active','selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')),
    to_branch_state TEXT NOT NULL CHECK (to_branch_state IN ('active','selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')),
    evidence_hash TEXT CHECK (evidence_hash IS NULL OR evidence_hash ~ '^[0-9a-f]{64}$'),
    reason_code TEXT,
    actor TEXT NOT NULL,
    event_hash TEXT NOT NULL CHECK (event_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_branch_events_node_fk
        FOREIGN KEY (node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (run_id, event_hash),
    CHECK (from_branch_state <> to_branch_state),
    CHECK (reason_code IS NULL OR length(trim(reason_code)) BETWEEN 1 AND 120),
    CHECK (length(trim(actor)) BETWEEN 1 AND 200),
    CHECK (to_branch_state <> 'active' OR evidence_hash IS NOT NULL)
);

CREATE INDEX decision_branch_events_node_idx
    ON decision_branch_events (run_id, node_id, created_at, id);

CREATE TABLE decision_graph_budgets (
    run_id BIGINT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    used_nodes BIGINT NOT NULL DEFAULT 0 CHECK (used_nodes >= 0),
    max_depth_observed BIGINT NOT NULL DEFAULT 0 CHECK (max_depth_observed >= 0),
    active_parallel_nodes BIGINT NOT NULL DEFAULT 0 CHECK (active_parallel_nodes >= 0),
    used_model_calls BIGINT NOT NULL DEFAULT 0 CHECK (used_model_calls >= 0),
    used_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (used_input_tokens >= 0),
    used_output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (used_output_tokens >= 0),
    used_replans BIGINT NOT NULL DEFAULT 0 CHECK (used_replans >= 0),
    used_verifications BIGINT NOT NULL DEFAULT 0 CHECK (used_verifications >= 0),
    used_wall_time_ms BIGINT NOT NULL DEFAULT 0 CHECK (used_wall_time_ms >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_graph_budgets_run_fk
        FOREIGN KEY (run_id, organization_id)
        REFERENCES decision_graph_runs(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (run_id, organization_id)
);

CREATE TABLE decision_node_executions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    graph_version_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    status TEXT NOT NULL CHECK (status IN ('claimed','running','waiting_verification','succeeded','failed','cancelled','ambiguous')),
    claim_token_hash TEXT NOT NULL CHECK (claim_token_hash ~ '^[0-9a-f]{64}$'),
    claimed_by TEXT NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL,
    claim_expires_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    model_invocation_id BIGINT,
    dispatch_attempt_id BIGINT,
    outcome_hash TEXT CHECK (outcome_hash IS NULL OR outcome_hash ~ '^[0-9a-f]{64}$'),
    reason_code TEXT,
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_node_executions_node_fk
        FOREIGN KEY (node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT decision_node_executions_invocation_fk
        FOREIGN KEY (model_invocation_id, organization_id)
        REFERENCES model_invocations(id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT decision_node_executions_dispatch_fk
        FOREIGN KEY (dispatch_attempt_id, model_invocation_id)
        REFERENCES model_dispatch_attempts(id, invocation_id)
        ON DELETE RESTRICT,
    UNIQUE (node_id, attempt_number),
    UNIQUE (id, node_id, run_id, organization_id),
    UNIQUE (id, run_id, organization_id),
    CHECK (length(trim(claimed_by)) BETWEEN 1 AND 200),
    CHECK (claim_expires_at > claimed_at),
    CHECK (started_at IS NULL OR started_at >= claimed_at),
    CHECK (finished_at IS NULL OR finished_at >= claimed_at),
    CHECK (reason_code IS NULL OR length(trim(reason_code)) BETWEEN 1 AND 120),
    CHECK ((model_invocation_id IS NULL) = (dispatch_attempt_id IS NULL)),
    CHECK ((status IN ('succeeded','failed','cancelled','ambiguous')) = (finished_at IS NOT NULL))
);

CREATE UNIQUE INDEX decision_node_executions_one_active_idx
    ON decision_node_executions (node_id)
    WHERE status IN ('claimed','running','waiting_verification');
CREATE INDEX decision_node_executions_recovery_idx
    ON decision_node_executions (claim_expires_at, id)
    WHERE status IN ('claimed','running','waiting_verification');

CREATE TABLE decision_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    execution_id BIGINT NOT NULL,
    schema_version TEXT NOT NULL,
    observation_hash TEXT NOT NULL CHECK (observation_hash ~ '^[0-9a-f]{64}$'),
    source_kind TEXT NOT NULL CHECK (source_kind ~ '^[a-z][a-z0-9_]{0,79}$'),
    source_reference_hash TEXT CHECK (source_reference_hash IS NULL OR source_reference_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_observations_execution_fk
        FOREIGN KEY (execution_id, node_id, run_id, organization_id)
        REFERENCES decision_node_executions(id, node_id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (execution_id, observation_hash),
    CHECK (length(trim(schema_version)) BETWEEN 1 AND 120)
);

CREATE TABLE decision_verifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    execution_id BIGINT,
    label TEXT NOT NULL CHECK (label IN ('verified','inferred','unknown','contradicted')),
    verifier_ref TEXT NOT NULL,
    verifier_version TEXT NOT NULL,
    evidence_set_hash TEXT NOT NULL CHECK (evidence_set_hash ~ '^[0-9a-f]{64}$'),
    reason_codes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(reason_codes) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_verifications_node_fk
        FOREIGN KEY (node_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, run_id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT decision_verifications_execution_fk
        FOREIGN KEY (execution_id, node_id, run_id, organization_id)
        REFERENCES decision_node_executions(id, node_id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (id, run_id, organization_id),
    CHECK (length(trim(verifier_ref)) BETWEEN 1 AND 200),
    CHECK (length(trim(verifier_version)) BETWEEN 1 AND 120)
);

CREATE INDEX decision_verifications_node_idx
    ON decision_verifications (run_id, node_id, created_at, id);

CREATE TABLE decision_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    graph_version_id BIGINT NOT NULL,
    decision_node_id BIGINT NOT NULL,
    selected_candidate_node_id BIGINT NOT NULL,
    evidence_set_hash TEXT NOT NULL CHECK (evidence_set_hash ~ '^[0-9a-f]{64}$'),
    verification_set_hash TEXT NOT NULL CHECK (verification_set_hash ~ '^[0-9a-f]{64}$'),
    decision_hash TEXT NOT NULL CHECK (decision_hash ~ '^[0-9a-f]{64}$'),
    verification_label TEXT NOT NULL CHECK (verification_label IN ('verified','inferred')),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_records_decision_node_fk
        FOREIGN KEY (decision_node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    CONSTRAINT decision_records_candidate_node_fk
        FOREIGN KEY (selected_candidate_node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (run_id),
    UNIQUE (decision_hash),
    CHECK (length(trim(created_by)) BETWEEN 1 AND 200)
);

CREATE TABLE decision_budget_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    event_kind TEXT NOT NULL CHECK (event_kind IN ('graph_appended','node_claimed','execution_finished','verification_recorded','replan_recorded','wall_time_recorded')),
    nodes_delta BIGINT NOT NULL DEFAULT 0 CHECK (nodes_delta >= 0),
    depth_observed BIGINT NOT NULL DEFAULT 0 CHECK (depth_observed >= 0),
    parallel_delta INTEGER NOT NULL DEFAULT 0 CHECK (parallel_delta IN (-1,0,1)),
    model_calls_delta BIGINT NOT NULL DEFAULT 0 CHECK (model_calls_delta >= 0),
    input_tokens_delta BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens_delta >= 0),
    output_tokens_delta BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens_delta >= 0),
    replans_delta BIGINT NOT NULL DEFAULT 0 CHECK (replans_delta >= 0),
    verifications_delta BIGINT NOT NULL DEFAULT 0 CHECK (verifications_delta >= 0),
    wall_time_ms_delta BIGINT NOT NULL DEFAULT 0 CHECK (wall_time_ms_delta >= 0),
    event_hash TEXT NOT NULL CHECK (event_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_budget_events_run_fk
        FOREIGN KEY (run_id, organization_id)
        REFERENCES decision_graph_runs(id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (run_id, event_hash)
);

CREATE FUNCTION decision_graph_reject_edge_cycle() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.edge_type <> 'depends_on' THEN
        RETURN NEW;
    END IF;
    IF EXISTS (
        WITH RECURSIVE reachable(node_id) AS (
            SELECT NEW.to_node_id
            UNION
            SELECT e.to_node_id
            FROM decision_graph_edges e
            JOIN reachable r ON e.from_node_id = r.node_id
            WHERE e.graph_version_id = NEW.graph_version_id
              AND e.edge_type = 'depends_on'
        )
        SELECT 1 FROM reachable WHERE node_id = NEW.from_node_id
    ) THEN
        RAISE EXCEPTION 'decision dependency cycle' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER decision_graph_edges_cycle_guard
BEFORE INSERT ON decision_graph_edges
FOR EACH ROW EXECUTE FUNCTION decision_graph_reject_edge_cycle();

CREATE FUNCTION decision_graph_guard_node_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id <> OLD.organization_id
       OR NEW.run_id <> OLD.run_id
       OR NEW.graph_version_id <> OLD.graph_version_id
       OR NEW.logical_node_id <> OLD.logical_node_id
       OR NEW.node_type <> OLD.node_type
       OR NEW.payload_schema_version <> OLD.payload_schema_version
       OR NEW.payload_hash <> OLD.payload_hash
       OR NEW.context_snapshot_id IS DISTINCT FROM OLD.context_snapshot_id
       OR NEW.depth <> OLD.depth
       OR NEW.created_by <> OLD.created_by
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'immutable decision node fields changed' USING ERRCODE = '23514';
    END IF;

    IF NEW.execution_state <> OLD.execution_state AND NOT (
        (OLD.execution_state = 'pending' AND NEW.execution_state IN ('ready','running','cancelled'))
        OR (OLD.execution_state = 'ready' AND NEW.execution_state IN ('running','cancelled'))
        OR (OLD.execution_state = 'running' AND NEW.execution_state IN ('waiting_verification','succeeded','failed','cancelled','ambiguous'))
        OR (OLD.execution_state = 'waiting_verification' AND NEW.execution_state IN ('succeeded','failed','cancelled','ambiguous'))
    ) THEN
        RAISE EXCEPTION 'invalid decision node execution transition: % -> %', OLD.execution_state, NEW.execution_state USING ERRCODE = '23514';
    END IF;

    IF NEW.branch_state <> OLD.branch_state AND NOT (
        (
            OLD.branch_state = 'active'
            AND NEW.branch_state IN ('selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')
        )
        OR (
            OLD.branch_state IN ('rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','inconclusive')
            AND NEW.branch_state = 'active'
            AND EXISTS (
                SELECT 1
                FROM decision_branch_events event
                WHERE event.node_id = OLD.id
                  AND event.run_id = OLD.run_id
                  AND event.organization_id = OLD.organization_id
                  AND event.graph_version_id = OLD.graph_version_id
                  AND event.from_branch_state = OLD.branch_state
                  AND event.to_branch_state = NEW.branch_state
                  AND event.evidence_hash IS NOT NULL
                  AND event.created_at = NEW.updated_at
            )
        )
    ) THEN
        RAISE EXCEPTION 'invalid decision branch transition: % -> %', OLD.branch_state, NEW.branch_state USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION decision_graph_guard_run_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id <> OLD.organization_id
       OR NEW.task_id <> OLD.task_id
       OR NEW.attempt_id <> OLD.attempt_id
       OR NEW.reasoning_policy_schema_version <> OLD.reasoning_policy_schema_version
       OR NEW.reasoning_policy_hash <> OLD.reasoning_policy_hash
       OR NEW.idempotency_key <> OLD.idempotency_key
       OR NEW.max_nodes <> OLD.max_nodes
       OR NEW.max_depth <> OLD.max_depth
       OR NEW.max_parallel_nodes <> OLD.max_parallel_nodes
       OR NEW.max_model_calls <> OLD.max_model_calls
       OR NEW.max_input_tokens <> OLD.max_input_tokens
       OR NEW.max_output_tokens <> OLD.max_output_tokens
       OR NEW.max_replans <> OLD.max_replans
       OR NEW.max_verifications <> OLD.max_verifications
       OR NEW.max_wall_time_ms <> OLD.max_wall_time_ms
       OR NEW.deadline <> OLD.deadline
       OR NEW.created_by <> OLD.created_by
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'immutable decision run fields changed' USING ERRCODE = '23514';
    END IF;

    IF NEW.status <> OLD.status AND NOT (
        (OLD.status = 'planned' AND NEW.status IN ('running','failed','cancelled'))
        OR (OLD.status = 'running' AND NEW.status IN ('waiting','succeeded','failed','cancelled','ambiguous'))
        OR (OLD.status = 'waiting' AND NEW.status IN ('running','succeeded','failed','cancelled','ambiguous'))
    ) THEN
        RAISE EXCEPTION 'invalid decision run transition: % -> %', OLD.status, NEW.status USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;
CREATE TRIGGER decision_graph_nodes_update_guard
BEFORE UPDATE ON decision_graph_nodes
FOR EACH ROW EXECUTE FUNCTION decision_graph_guard_node_update();
CREATE TRIGGER decision_graph_runs_update_guard
BEFORE UPDATE ON decision_graph_runs
FOR EACH ROW EXECUTE FUNCTION decision_graph_guard_run_update();

CREATE FUNCTION decision_graph_immutable_row() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'decision graph ledger row is immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER decision_branch_events_immutable
BEFORE UPDATE OR DELETE ON decision_branch_events
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_graph_versions_immutable
BEFORE UPDATE OR DELETE ON decision_graph_versions
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_graph_edges_immutable
BEFORE UPDATE OR DELETE ON decision_graph_edges
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_observations_immutable
BEFORE UPDATE OR DELETE ON decision_observations
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_verifications_immutable
BEFORE UPDATE OR DELETE ON decision_verifications
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_records_immutable
BEFORE UPDATE OR DELETE ON decision_records
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_budget_events_immutable
BEFORE UPDATE OR DELETE ON decision_budget_events
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
