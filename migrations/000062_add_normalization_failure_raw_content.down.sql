-- Reverses migration 000062. Dropping a diagnostic-only, nullable column
-- that carries no invariant anything else depends on is safe and
-- non-destructive to any other data.
ALTER TABLE model_provider_outcomes
    DROP COLUMN IF EXISTS normalization_failure_raw_content;
