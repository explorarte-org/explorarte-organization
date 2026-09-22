-- Migration 000081: let FailAfterResponse actually attach the diagnostic content
-- migration 000062 built a column for.
--
-- migration 000011's model_provider_outcomes_no_mutation trigger rejects EVERY
-- UPDATE, unconditionally, with no exception. migration 000062 then added
-- normalization_failure_raw_content and FailAfterResponse's one UPDATE that
-- writes it (internal/modelruntime/postgres/results.go, G3-004) -- but the
-- trigger from 000011 was never told about it. The result: whenever a model's
-- response transports fine but fails the host's own JSON normalization,
-- FailAfterResponse's UPDATE is rejected by the trigger it was written to use
-- (SQLSTATE P0001, "model provider outcomes are immutable"), the whole
-- transaction rolls back, and the invocation is left stuck at
-- response_received forever -- never marked failed, its cost reservation
-- never settled, the very diagnostic content 000062 exists to capture never
-- recorded. Production root 1147 (2026-09-22) hit exactly this: an
-- otherwise-ordinary "the model's JSON was invalid" failure, made worse by a
-- trigger blocking its own column's only writer.
--
-- The fix is narrow, not a rollback of 000011's intent: everything about a
-- recorded outcome -- what was sent, what came back, how it was classified --
-- stays permanently fixed. The ONE thing now permitted is setting
-- normalization_failure_raw_content from NULL to a value, exactly once, on a
-- row whose every other REGULAR column is unchanged. Once set, that field is
-- immutable too: a second attempt to write it (or any other field) is still
-- rejected identically to before. DELETE stays rejected outright, as it
-- always was.
CREATE OR REPLACE FUNCTION reject_model_provider_outcome_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'model provider outcomes are immutable';
    END IF;

    -- The one column this table's own diagnostic-content writer (G3-004,
    -- migration 000062) may set, and only while it has never been set before.
    IF OLD.normalization_failure_raw_content IS NOT NULL THEN
        RAISE EXCEPTION 'model provider outcomes are immutable: normalization_failure_raw_content is already set';
    END IF;

    -- Whole-row comparison of everything else, done in jsonb rather than by
    -- composite equality so two columns can be dropped from both sides before
    -- comparing: normalization_failure_raw_content (the one field this UPDATE
    -- may actually change) and provider_reached. provider_reached is
    -- GENERATED ALWAYS AS (adapter_failure_phase IS DISTINCT FROM
    -- 'before_request') STORED -- PostgreSQL leaves a generated column's
    -- value UNSPECIFIED on NEW inside a BEFORE UPDATE trigger (it is computed
    -- afterward, once every BEFORE trigger has run), so comparing it here
    -- would reject every legitimate write on a spurious mismatch, not catch a
    -- real one: nothing can target a generated column directly (PostgreSQL
    -- itself refuses that at parse time), and the one column it is derived
    -- from, adapter_failure_phase, is still covered by this same comparison.
    -- A future generated column on this table needs the same exclusion.
    IF (to_jsonb(NEW) - 'normalization_failure_raw_content' - 'provider_reached')
       IS DISTINCT FROM
       (to_jsonb(OLD) - 'normalization_failure_raw_content' - 'provider_reached')
    THEN
        RAISE EXCEPTION 'model provider outcomes are immutable';
    END IF;

    RETURN NEW;
END;
$$;
