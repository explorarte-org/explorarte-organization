-- External audit A4 (2026-09-27): a proposal's acceptance criteria are stored as text, and promotion
-- used to classify every one of them as implementation -- including criteria about the design. The
-- phase of each criterion is now declared with the proposal, in the same order as its criteria.
-- NULL for proposals made before this column existed; their promotion keeps the historical mapping.
ALTER TABLE campaign_proposals
    ADD COLUMN acceptance_criterion_phases JSONB NULL
        CHECK (acceptance_criterion_phases IS NULL OR jsonb_typeof(acceptance_criterion_phases) = 'array');
