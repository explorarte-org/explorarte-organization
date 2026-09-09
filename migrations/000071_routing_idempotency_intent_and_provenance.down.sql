ALTER TABLE model_invocations
    DROP COLUMN IF EXISTS idempotency_intent_hash,
    DROP COLUMN IF EXISTS routing_policy_id,
    DROP COLUMN IF EXISTS routing_candidate_hash,
    DROP COLUMN IF EXISTS routing_decision_reason;
