CREATE TABLE audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) > 0),
    actor_type TEXT,
    actor_id TEXT,
    subject_type TEXT,
    subject_id TEXT,
    correlation_id TEXT,
    causation_id TEXT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_events_occurred_at_idx
    ON audit_events (occurred_at DESC);

CREATE INDEX audit_events_event_type_occurred_at_idx
    ON audit_events (event_type, occurred_at DESC);

CREATE INDEX audit_events_correlation_id_idx
    ON audit_events (correlation_id)
    WHERE correlation_id IS NOT NULL;
