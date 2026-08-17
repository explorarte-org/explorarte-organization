DROP INDEX IF EXISTS model_invocation_render_telemetry_execution_context_view_id_idx;

ALTER TABLE model_invocation_render_telemetry
    DROP COLUMN execution_context_view_id,
    DROP COLUMN token_estimator_id,
    DROP COLUMN token_estimator_version,
    DROP COLUMN estimated_provider_visible_tokens,
    DROP COLUMN estimated_stable_prefix_tokens,
    DROP COLUMN estimated_dynamic_suffix_tokens,
    DROP COLUMN segment_token_estimates;
