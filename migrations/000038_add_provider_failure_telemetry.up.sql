-- Gate F (Provider Failure Telemetry, originally P0-D): the only remaining
-- P0 from this branch's audit. It was explicitly skipped by the P0-A agent
-- because ProviderOutcome's natural home,
-- internal/modelruntime/provider_adapter.go, was outside that change's file
-- ownership. See the companion Go changes:
--   internal/modelruntime/provider_adapter.go   (ProviderOutcome fields + Validate)
--   internal/modelruntime/adapter/deepseek/adapter.go (failureTelemetry, populated
--     at every point a ProviderOutcome is constructed)
--   internal/modelruntime/postgres/results.go   (insertProviderOutcome persistence)
--
-- Goal: for every failed invocation, consultably distinguish WHY it failed
-- and WHERE in the call it failed, without ever storing the prompt,
-- completion, hidden reasoning, or any secret. Every column below is a
-- length, a byte count, a token count, a provider-supplied enum-like token,
-- or a request-shaping fact (response format / max output tokens) that is
-- already public API surface -- never response content.
--
-- adapter_failure_phase mirrors modelruntime.AdapterFailurePhase
-- (before_request / response_received / ambiguous_after_request) -- which
-- of the three phases this outcome was observed at, set by the Store method
-- persisting it (see insertProviderOutcome's doc comment), since it follows
-- from which state transition is being persisted, not from anything
-- decodable off the outcome value itself.
--
-- provider_reached is intentionally a GENERATED column, not written
-- directly by application code: it must always be derived 1:1 from
-- adapter_failure_phase <> 'before_request' (a NULL phase -- the ordinary
-- success path via MarkResponseReceived, which never carries a phase --
-- means the response was received, hence reached=true), and a generated
-- column is the only way to make that invariant impossible to violate by
-- accident from Go.
ALTER TABLE model_provider_outcomes
    ADD COLUMN adapter_failure_phase TEXT,
    ADD COLUMN provider_reached BOOLEAN GENERATED ALWAYS AS (
        adapter_failure_phase IS DISTINCT FROM 'before_request'
    ) STORED,
    ADD COLUMN finish_reason TEXT,
    ADD COLUMN response_content_bytes INTEGER,
    ADD COLUMN usage_available BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN input_tokens BIGINT,
    ADD COLUMN output_tokens BIGINT,
    ADD COLUMN cache_hit_tokens BIGINT,
    ADD COLUMN cache_miss_tokens BIGINT,
    ADD COLUMN response_format TEXT,
    ADD COLUMN max_output_tokens INTEGER,
    ADD COLUMN request_duration_ms BIGINT,
    -- JSON parse failures only (error_code='response_json_invalid'): the Go
    -- encoding/json error's offset and type name (never the JSON body
    -- itself), plus two cheap boundary checks on the raw bytes that
    -- distinguish "provider sent something that isn't JSON at all" from
    -- "provider sent JSON that was truncated mid-object". error_code itself
    -- already exists on this table (added in 000011) and is reused as the
    -- generic error_classification signal -- no new column duplicates it.
    ADD COLUMN json_error_class TEXT,
    ADD COLUMN json_error_offset BIGINT,
    ADD COLUMN starts_with_json_object BOOLEAN,
    ADD COLUMN ends_with_json_object BOOLEAN;

ALTER TABLE model_provider_outcomes ALTER COLUMN usage_available DROP DEFAULT;

ALTER TABLE model_provider_outcomes
    ADD CONSTRAINT model_provider_outcomes_adapter_failure_phase_check CHECK (
        adapter_failure_phase IS NULL OR adapter_failure_phase IN (
            'before_request', 'response_received', 'ambiguous_after_request'
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_finish_reason_check CHECK (
        finish_reason IS NULL OR length(trim(finish_reason)) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT model_provider_outcomes_response_content_bytes_check CHECK (
        response_content_bytes IS NULL OR response_content_bytes >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_input_tokens_check CHECK (
        input_tokens IS NULL OR input_tokens >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_output_tokens_check CHECK (
        output_tokens IS NULL OR output_tokens >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_cache_hit_tokens_check CHECK (
        cache_hit_tokens IS NULL OR cache_hit_tokens >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_cache_miss_tokens_check CHECK (
        cache_miss_tokens IS NULL OR cache_miss_tokens >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_usage_available_check CHECK (
        NOT usage_available OR input_tokens IS NOT NULL OR output_tokens IS NOT NULL
    ),
    ADD CONSTRAINT model_provider_outcomes_response_format_check CHECK (
        response_format IS NULL OR length(trim(response_format)) BETWEEN 1 AND 60
    ),
    ADD CONSTRAINT model_provider_outcomes_max_output_tokens_check CHECK (
        max_output_tokens IS NULL OR max_output_tokens > 0
    ),
    ADD CONSTRAINT model_provider_outcomes_request_duration_ms_check CHECK (
        request_duration_ms IS NULL OR request_duration_ms >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_json_error_class_check CHECK (
        json_error_class IS NULL OR length(trim(json_error_class)) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT model_provider_outcomes_json_error_offset_check CHECK (
        json_error_offset IS NULL OR json_error_offset >= 0
    ),
    ADD CONSTRAINT model_provider_outcomes_json_error_offset_class_check CHECK (
        json_error_offset IS NULL OR json_error_class IS NOT NULL
    );
