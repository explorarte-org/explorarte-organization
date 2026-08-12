ALTER TABLE model_dispatch_attempts
    DROP CONSTRAINT model_dispatch_attempts_runtime_build_sha_check;

ALTER TABLE model_dispatch_attempts
    DROP COLUMN runtime_build_sha;

ALTER TABLE model_invocation_usage
    DROP CONSTRAINT model_invocation_usage_cache_miss_check,
    DROP CONSTRAINT model_invocation_usage_cache_hit_check;

ALTER TABLE model_invocation_usage
    DROP COLUMN prompt_cache_hit_tokens,
    DROP COLUMN prompt_cache_miss_tokens;

-- Restore the original, fully append-only trigger from migration 000021.
CREATE OR REPLACE FUNCTION reject_provider_wallet_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'provider wallet events are immutable';
END;
$$;

ALTER TABLE provider_wallet_events
    DROP CONSTRAINT provider_wallet_events_financial_outcome_check,
    DROP CONSTRAINT provider_wallet_events_cost_provenance_check;

ALTER TABLE provider_wallet_events
    DROP COLUMN cost_provenance,
    DROP COLUMN financial_outcome;
