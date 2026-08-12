-- P0 cost-ledger fix: today's DeepSeek corpus-curation canary showed the
-- internal ledger drastically undercounts real spend (DeepSeek's own
-- dashboard: 113 requests / 9.37M tokens / $1.36; our ledger only recorded
-- the 16 invocations that ended 'succeeded', ~$0.20). Root cause: dispatch_service.go
-- released the cost reservation for AdapterFailureResponseReceived (the
-- provider DID respond -- HTTP headers+body were received, so the call may
-- already be billed) because that phase never set reservationSettled=true,
-- so the deferred Release fired unconditionally. Fixed in this same change
-- set in internal/modelruntime/dispatch_service.go.
--
-- These columns record, per wallet event, WHY the amount is what it is,
-- orthogonal to event kind (reserved/committed/released) per owner design:
-- a call can be business-`failed` (bad JSON, truncated content, ...) and
-- still be financially `actual` (the provider processed and likely billed
-- it) -- task success and financial settlement are independent facts.
--
-- cost_provenance: where the amount came from.
--   actual_provider_reported -- computed from real usage.prompt_tokens/
--     completion_tokens the provider's response envelope reported.
--   estimated_locally        -- the conservative worst-case estimate from
--     CostBudgetGate.Reserve (internal/modelruntime/dispatch_service.go's
--     estimateTokenCount), kept because real usage could not be recovered.
--   reconciled_provider      -- a later, out-of-band reconciliation job
--     corrected this amount against the provider's own billing records
--     (no such job exists yet; the value is reserved for it).
--   unknown                  -- not applicable / not yet known (e.g. a
--     released-before-send reservation was never priced against real usage).
-- financial_outcome: what ultimately happened to the reserved amount.
--   released_not_sent               -- request never reached the provider
--     (AdapterFailureBeforeRequest, or an unambiguously-unsent adapter
--     error); the reservation was freed, no money at risk.
--   actual                          -- committed at a real, provider-
--     reported cost (whether the invocation itself succeeded or failed).
--   estimated_pending_reconciliation -- provider receipt is certain or
--     likely (AdapterFailureAmbiguous, a response was received but no
--     usage could be recovered from it, a transport timeout after send,
--     or a local persistence failure after the provider already replied)
--     but the real cost is unknown; the reservation stays parked at its
--     conservative estimate rather than being released as if free.
--   reconciled                      -- reserved for the same future
--     reconciliation job as cost_provenance=reconciled_provider.
--
-- Both columns are NULL on a 'reserved' event until its outcome is known
-- (Reserve() cannot know the outcome yet) and are populated at INSERT time
-- for 'committed'/'released' events. The one exception: a 'reserved' event
-- that is going to stay reserved pending reconciliation (never committed or
-- released) needs its provenance/outcome set onto the SAME already-written
-- row -- the wallet ledger is append-only by design (see migration 000021's
-- provider_wallet_events_no_mutation trigger) and a second 'reserved' event
-- for the same (provider_id, invocation_id) is exactly what
-- provider_wallet_events_unique_kind forbids. The trigger below is relaxed
-- to allow exactly that one annotation, once, and nothing else: kind,
-- amount_usd_nanos, provider_id, invocation_id, embedding_invocation_id and
-- created_at remain fully immutable, and an already-annotated row can never
-- be re-annotated.
ALTER TABLE provider_wallet_events
    ADD COLUMN cost_provenance TEXT,
    ADD COLUMN financial_outcome TEXT;

ALTER TABLE provider_wallet_events
    ADD CONSTRAINT provider_wallet_events_cost_provenance_check
    CHECK (cost_provenance IS NULL OR cost_provenance IN ('actual_provider_reported', 'estimated_locally', 'reconciled_provider', 'unknown')),
    ADD CONSTRAINT provider_wallet_events_financial_outcome_check
    CHECK (financial_outcome IS NULL OR financial_outcome IN ('released_not_sent', 'actual', 'estimated_pending_reconciliation', 'reconciled'));

CREATE OR REPLACE FUNCTION reject_provider_wallet_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'provider wallet events are immutable';
    END IF;
    IF NEW.kind IS DISTINCT FROM OLD.kind
        OR NEW.amount_usd_nanos IS DISTINCT FROM OLD.amount_usd_nanos
        OR NEW.provider_id IS DISTINCT FROM OLD.provider_id
        OR NEW.invocation_id IS DISTINCT FROM OLD.invocation_id
        OR NEW.embedding_invocation_id IS DISTINCT FROM OLD.embedding_invocation_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.cost_provenance IS NOT NULL
        OR OLD.financial_outcome IS NOT NULL
    THEN
        RAISE EXCEPTION 'provider wallet events are immutable except a one-time cost_provenance/financial_outcome annotation on a row that has neither set yet';
    END IF;
    RETURN NEW;
END;
$$;

-- DeepSeek's usage object can report a prompt-cache hit/miss split
-- (prompt_cache_hit_tokens / prompt_cache_miss_tokens); today it is decoded
-- and immediately discarded. Nullable: the provider may omit either or both
-- fields, and an absent field must be stored as NULL, never fabricated as
-- zero. Also extended to allow one usage row per invocation even when the
-- invocation's own business outcome was 'failed' -- see companion change in
-- internal/modelruntime/postgres/results.go, which now inserts a usage row
-- from RejectProviderResponse/FailAfterResponse whenever the provider's
-- response envelope carried recoverable token counts, not only from
-- CompleteInvocation's success path.
ALTER TABLE model_invocation_usage
    ADD COLUMN prompt_cache_hit_tokens BIGINT,
    ADD COLUMN prompt_cache_miss_tokens BIGINT;

ALTER TABLE model_invocation_usage
    ADD CONSTRAINT model_invocation_usage_cache_hit_check
    CHECK (prompt_cache_hit_tokens IS NULL OR prompt_cache_hit_tokens >= 0),
    ADD CONSTRAINT model_invocation_usage_cache_miss_check
    CHECK (prompt_cache_miss_tokens IS NULL OR prompt_cache_miss_tokens >= 0);

-- P0-F (runtime/adapter build identity): first-cut column only. Populating
-- it end-to-end needs a build-time -ldflags "-X ...buildSHA=$(git rev-parse
-- HEAD)" wired into the real Docker build (Dockerfile/compose.yaml were
-- outside this change's file ownership to verify safely), so today this
-- column is written with the package-level modelruntime.BuildSHA default
-- ("unknown") until that ldflags wiring lands as a followup. See the
-- companion Go change (internal/modelruntime/domain.go: var BuildSHA).
ALTER TABLE model_dispatch_attempts
    ADD COLUMN runtime_build_sha TEXT;

ALTER TABLE model_dispatch_attempts
    ADD CONSTRAINT model_dispatch_attempts_runtime_build_sha_check
    CHECK (runtime_build_sha IS NULL OR length(runtime_build_sha) BETWEEN 1 AND 120);
