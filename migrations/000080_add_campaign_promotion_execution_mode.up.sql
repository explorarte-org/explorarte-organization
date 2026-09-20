-- CAMPAIGN_GOVERNED_EXECUTION_MODE_V1: durable provenance of the execution mode a
-- campaign promotion ran under.
--
-- Additive and default-preserving: every promotion that exists today was made
-- before the mode existed and ran analysis_only, which is exactly the default, so
-- no backfill is needed and no historical row changes meaning. The mode is written
-- once with the promotion and the table is append-only in practice (one promotion
-- per owner approval), so the mode an owner chose is the mode the campaign kept.

ALTER TABLE campaign_promotions
    ADD COLUMN IF NOT EXISTS execution_mode TEXT NOT NULL DEFAULT 'analysis_only'
        CONSTRAINT campaign_promotions_execution_mode_check
        CHECK (execution_mode IN ('analysis_only', 'governed_implementation'));
