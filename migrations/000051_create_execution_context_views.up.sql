CREATE TABLE execution_context_views (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    context_snapshot_id BIGINT NOT NULL,
    context_profile_id TEXT NOT NULL DEFAULT '',
    context_profile_version TEXT NOT NULL DEFAULT '',
    fell_back_to_canonical BOOLEAN NOT NULL,
    fallback_reason TEXT NOT NULL DEFAULT '',
    provider_render_version TEXT NOT NULL DEFAULT '',
    stable_prefix_hash TEXT NOT NULL DEFAULT '',
    stable_prefix_bytes INTEGER NOT NULL DEFAULT 0 CHECK (stable_prefix_bytes >= 0),
    dynamic_suffix_hash TEXT NOT NULL DEFAULT '',
    dynamic_suffix_bytes INTEGER NOT NULL DEFAULT 0 CHECK (dynamic_suffix_bytes >= 0),
    authority_order_hash TEXT NOT NULL CHECK (authority_order_hash ~ '^[0-9a-f]{64}$'),
    compiled_content_hash TEXT NOT NULL CHECK (compiled_content_hash ~ '^[0-9a-f]{64}$'),
    segment_diffs JSONB NOT NULL CHECK (jsonb_typeof(segment_diffs) = 'array'),
    provider_visible_bytes BYTEA NOT NULL,
    provider_visible_digest TEXT NOT NULL CHECK (provider_visible_digest ~ '^[0-9a-f]{64}$'),
    provider_visible_byte_count INTEGER NOT NULL CHECK (provider_visible_byte_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (octet_length(provider_visible_bytes) = provider_visible_byte_count),
    CHECK (octet_length(provider_visible_bytes) BETWEEN 1 AND 8388608),
    -- Exactly one durable view per canonical snapshot: this is the
    -- idempotency/collision-prevention boundary. ResolveAndPersist relies
    -- on this constraint (INSERT ... ON CONFLICT (context_snapshot_id))
    -- to decide between "return the existing idempotent view" and
    -- "reject drift" without a race between concurrent callers.
    UNIQUE (context_snapshot_id),
    -- Binds every durable view to the exact (snapshot, organization) pair
    -- context_snapshots itself proves -- a view can never be created
    -- against a snapshot belonging to a different organization, and this
    -- is enforced by PostgreSQL, not only by application code.
    CONSTRAINT execution_context_views_snapshot_org_fk
        FOREIGN KEY (context_snapshot_id, organization_id)
        REFERENCES context_snapshots (id, organization_id)
        ON DELETE RESTRICT
);

CREATE INDEX execution_context_views_org_idx ON execution_context_views (organization_id, context_snapshot_id);

CREATE OR REPLACE FUNCTION reject_execution_context_view_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'execution context views are immutable';
END;
$$;

CREATE TRIGGER execution_context_views_immutable
BEFORE UPDATE OR DELETE ON execution_context_views
FOR EACH ROW EXECUTE FUNCTION reject_execution_context_view_mutation();
