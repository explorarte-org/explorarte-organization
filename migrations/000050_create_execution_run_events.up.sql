-- Migration 000050: durable Execution Harness run history.
--
-- The Harness owns cognitive trajectory and already treats its history as an
-- append-only, sequence-ordered ledger; until now the only implementation was
-- in memory, so a process restart lost the trajectory and a resumed run could
-- repeat a model turn or a tool side effect. This table is that ledger made
-- durable. It deliberately stores no task state, no authority decision and no
-- pricing: those live in their own tables and remain their owners' truth.
--
-- Ordering is an explicit per-run ordinal, never a timestamp: clock_timestamp()
-- is not monotonic enough to reconstruct a trajectory, and two events written
-- inside the same millisecond must still have an unambiguous order.
CREATE TABLE execution_run_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    run_id TEXT NOT NULL CHECK (run_id <> '' AND length(run_id) <= 200),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    attempt_id BIGINT NOT NULL CHECK (attempt_id > 0),
    event_type TEXT NOT NULL CHECK (event_type <> ''),
    correlation_id TEXT NOT NULL,
    causation_id TEXT NOT NULL,
    terminal_status TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    -- The run ordinal is unique per organization, so two organizations may use
    -- the same run identifier without ever sharing or interleaving a history.
    -- This constraint is also the read index: history is always fetched as the
    -- ordered prefix of one run.
    CONSTRAINT execution_run_events_run_sequence_unique UNIQUE (organization_id, run_id, sequence)
);

-- Append-only, matching every other durable ledger in this schema
-- (audit_events, context_segments, model_egress_*, ...): a bug, a compromised
-- application role, or ad-hoc SQL must not be able to rewrite a trajectory
-- that the Harness will later replay as truth.
CREATE OR REPLACE FUNCTION execution_run_events_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'execution_run_events rows are append-only' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER execution_run_events_immutable
    BEFORE UPDATE OR DELETE ON execution_run_events
    FOR EACH ROW EXECUTE FUNCTION execution_run_events_reject_mutation();
