-- Restores the rule that could not record an incomplete read.
--
-- The constraint comes back NOT VALID on purpose. A rollback must be able to
-- run on a database that already contains what this migration made
-- recordable: an ambiguous outcome carrying the status and partial hash of a
-- response that stopped arriving. Validating it would fail on exactly those
-- rows, and the alternative -- deleting them -- would destroy the durable
-- record of a call that may already have been billed, which is the whole
-- reason the outcome is written down.
--
-- So the old rule governs new writes again, and observations already made are
-- kept. A rollback should undo a decision, not erase evidence gathered while
-- it was in force.

ALTER TABLE model_provider_outcomes DROP CONSTRAINT model_provider_outcomes_ambiguous_check;

ALTER TABLE model_provider_outcomes ADD CONSTRAINT model_provider_outcomes_ambiguous_check
    CHECK (
        outcome_classification <> 'ambiguous_transport'
        OR (http_status IS NULL AND response_hash IS NULL
            AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)
    ) NOT VALID;
