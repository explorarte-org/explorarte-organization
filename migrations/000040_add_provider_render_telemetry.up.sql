-- R10.4: ProviderRender v1 telemetry. Separates the provider-visible
-- render (StablePrefix + DynamicSuffix, what is actually sent to the
-- model) from the AuditEnvelope (snapshot_id/task_ref/request_hash/etc,
-- already persisted on model_invocations and context_snapshots -- never
-- duplicated here). This table exists purely to make prefix-cache
-- behavior observable per invocation: stable_prefix_hash constant across
-- invocations of the same actor/task-class/profile is the signal that
-- ProviderRender v1 is producing a byte-identical, cacheable prefix.
--
-- One row per invocation, written best-effort right after the render is
-- computed for dispatch (never blocks dispatch on a write failure --
-- this is observability, not a correctness gate; the real correctness
-- gate remains the existing context_render_hash_mismatch check against
-- model_invocations.request_hash / the snapshot's canonical RenderedHash,
-- untouched by this migration).
CREATE TABLE model_invocation_render_telemetry (
    invocation_id           BIGINT PRIMARY KEY REFERENCES model_invocations(id),
    provider_render_version TEXT NOT NULL,
    fallback_to_legacy       BOOLEAN NOT NULL DEFAULT false,
    fallback_reason          TEXT,
    stable_prefix_hash       TEXT NOT NULL,
    stable_prefix_bytes      INTEGER NOT NULL CHECK (stable_prefix_bytes >= 0),
    dynamic_suffix_hash      TEXT NOT NULL,
    dynamic_suffix_bytes     INTEGER NOT NULL CHECK (dynamic_suffix_bytes >= 0),
    provider_render_hash     TEXT NOT NULL,
    provider_visible_bytes   INTEGER NOT NULL CHECK (provider_visible_bytes >= 0),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX model_invocation_render_telemetry_stable_prefix_hash_idx
    ON model_invocation_render_telemetry (stable_prefix_hash);
