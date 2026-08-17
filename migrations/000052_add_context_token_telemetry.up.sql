-- M1.2: versioned host-side token telemetry for the exact durable
-- ExecutionContextView (000051), layered onto the SAME
-- model_invocation_render_telemetry row R10.4 (000040) already owns per
-- invocation, rather than a second, parallel telemetry table for what is
-- fundamentally the same observability record. Every new column is
-- nullable and additive: every historical R10.4-only row remains valid,
-- with these columns simply NULL until a productive dispatch records M1.2
-- telemetry for it.
ALTER TABLE model_invocation_render_telemetry
    ADD COLUMN execution_context_view_id BIGINT REFERENCES execution_context_views(id),
    ADD COLUMN token_estimator_id TEXT,
    ADD COLUMN token_estimator_version TEXT,
    ADD COLUMN estimated_provider_visible_tokens BIGINT,
    ADD COLUMN estimated_stable_prefix_tokens BIGINT,
    ADD COLUMN estimated_dynamic_suffix_tokens BIGINT,
    ADD COLUMN segment_token_estimates JSONB;

ALTER TABLE model_invocation_render_telemetry
    ADD CONSTRAINT model_invocation_render_telemetry_est_provider_tokens_nonneg
        CHECK (estimated_provider_visible_tokens IS NULL OR estimated_provider_visible_tokens >= 0),
    ADD CONSTRAINT model_invocation_render_telemetry_est_stable_tokens_nonneg
        CHECK (estimated_stable_prefix_tokens IS NULL OR estimated_stable_prefix_tokens >= 0),
    ADD CONSTRAINT model_invocation_render_telemetry_est_dynamic_tokens_nonneg
        CHECK (estimated_dynamic_suffix_tokens IS NULL OR estimated_dynamic_suffix_tokens >= 0),
    ADD CONSTRAINT model_invocation_render_telemetry_segment_estimates_array
        CHECK (segment_token_estimates IS NULL OR jsonb_typeof(segment_token_estimates) = 'array'),
    -- The M1.2 columns are written together, by a single query
    -- (RecordContextTokenTelemetry), or not at all: this constraint makes
    -- a partially-populated row impossible at the database layer, not
    -- only by Go-layer discipline.
    ADD CONSTRAINT model_invocation_render_telemetry_token_estimator_complete
        CHECK (
            (execution_context_view_id IS NULL) = (token_estimator_id IS NULL)
            AND (token_estimator_id IS NULL) = (token_estimator_version IS NULL)
            AND (token_estimator_id IS NULL) = (estimated_provider_visible_tokens IS NULL)
            AND (token_estimator_id IS NULL) = (estimated_stable_prefix_tokens IS NULL)
            AND (token_estimator_id IS NULL) = (estimated_dynamic_suffix_tokens IS NULL)
            AND (token_estimator_id IS NULL) = (segment_token_estimates IS NULL)
        );

CREATE INDEX model_invocation_render_telemetry_execution_context_view_id_idx
    ON model_invocation_render_telemetry (execution_context_view_id);
