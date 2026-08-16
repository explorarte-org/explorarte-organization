ALTER TABLE model_invocations
    ADD CONSTRAINT model_invocations_id_context_unique UNIQUE (id, context_snapshot_id);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM model_invocations
        WHERE status IN ('requested', 'claimed', 'send_started', 'response_received')
    ) THEN
        RAISE EXCEPTION 'cannot install model input envelopes while nonterminal model invocations exist';
    END IF;
END;
$$;

CREATE TABLE model_invocation_inputs (
    invocation_id BIGINT PRIMARY KEY,
    context_snapshot_id BIGINT NOT NULL REFERENCES context_snapshots(id) ON DELETE RESTRICT,
    schema_version TEXT NOT NULL,
    canonical_bytes BYTEA NOT NULL,
    canonical_digest TEXT NOT NULL CHECK (canonical_digest ~ '^[0-9a-f]{64}$'),
    canonical_projection_digest TEXT NOT NULL CHECK (canonical_projection_digest ~ '^[0-9a-f]{64}$'),
    stable_prefix_digest TEXT NOT NULL CHECK (stable_prefix_digest ~ '^[0-9a-f]{64}$'),
    input_classifications JSONB NOT NULL CHECK (jsonb_typeof(input_classifications) = 'array'),
    input_classifications_hash TEXT NOT NULL CHECK (input_classifications_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (invocation_id, canonical_digest),
    CHECK (schema_version = 'modelruntime.input.v1'),
    CHECK (octet_length(canonical_bytes) BETWEEN 1 AND 8388608),
    CONSTRAINT model_invocation_inputs_invocation_context_fk
        FOREIGN KEY (invocation_id, context_snapshot_id)
        REFERENCES model_invocations(id, context_snapshot_id)
        ON DELETE RESTRICT
);

CREATE OR REPLACE FUNCTION reject_model_invocation_input_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'model invocation inputs are immutable';
END;
$$;

CREATE TRIGGER model_invocation_inputs_immutable
BEFORE UPDATE OR DELETE ON model_invocation_inputs
FOR EACH ROW EXECUTE FUNCTION reject_model_invocation_input_mutation();
