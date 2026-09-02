-- Migration 000062: bounded raw response content on normalization failure (G3-004).
--
-- model_provider_outcomes is deliberately metadata-only everywhere else --
-- byte counts, token counts, enum-like classification strings, never
-- prompt/completion CONTENT (see insertProviderOutcome's own doc comment).
-- This column is a narrow, documented exception to that principle: when
-- Model Runtime's own host-side JSON normalization fails on an already-
-- decoded, already-received provider response (dispatch_service.go's
-- Normalize() call, error_code='response_normalization_failed' on the
-- invocation), the raw content the provider actually sent is otherwise
-- gone forever the moment the request completes -- exactly what blocked
-- root-causing invocation 134/140 (ORGANIZATION-GRAND-AUDIT-001, G3-004):
-- Gate F telemetry could prove finish_reason=length and 65,521 output
-- tokens, but nobody could ever see what those tokens actually were.
--
-- Bounded to 16KB (a bounded PREFIX, not the full response) -- enough to
-- see the shape of a runaway/repetition/truncation pattern without this
-- diagnostic-only column turning a high-traffic telemetry table into an
-- unbounded content store. NULL in every ordinary row; only ever set by
-- FailAfterResponse, only for this one failure class.
ALTER TABLE model_provider_outcomes
    ADD COLUMN IF NOT EXISTS normalization_failure_raw_content BYTEA;

COMMENT ON COLUMN model_provider_outcomes.normalization_failure_raw_content IS
    'Bounded (<=16KB) prefix of the raw provider response content, set ONLY when host-side normalization (dispatch_service.go Normalize()) failed on an already-received response. NULL otherwise. Deliberate, narrow exception to this table''s metadata-only design -- see migration 000062.';
