ALTER TABLE model_invocations
    DROP COLUMN IF EXISTS routing_mode,
    DROP COLUMN IF EXISTS routing_selector_id,
    DROP COLUMN IF EXISTS routing_candidate_set_hash;

DROP INDEX IF EXISTS routing_candidates_policy_idx;
DROP TABLE IF EXISTS routing_candidates;
DROP TABLE IF EXISTS routing_policies;
