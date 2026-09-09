-- Migration 000071: corrective round for Dynamic Canonical Model Routing.
--
-- Purely additive on top of 000070 -- no column from 000070 or any earlier
-- migration is altered, dropped, or renamed. 000070 already shipped
-- published (kernel/dynamic-canonical-model-routing is pushed), so this
-- adds rather than rewrites history.
--
-- idempotency_intent_hash: the CALLER's logical-request identity, computed
-- BEFORE route resolution (internal/modelruntime/hashing.go:
-- idempotencyIntentHash). CreateInvocation's ON CONFLICT(organization_id,
-- idempotency_key) path compares THIS, never request_hash, to decide
-- whether a retry may replay the existing row -- request_hash covers the
-- resolved route too (profile/version/provider/model) and is restored to
-- do so again in this round, so it can no longer be what a
-- capacity-state-driven retry is checked against.
--
-- Nullable at the column level: every row from before this migration has
-- no way to be backfilled with a value that was never computed for it.
-- Application code (postgres/invocations.go) is what enforces "every NEW
-- row always gets one, and an existing row found with NULL here fails
-- CLOSED (ErrConflict) rather than being trusted or silently overwritten" --
-- a NOT NULL constraint here would either reject 000070-era rows outright
-- (data loss) or require inventing a value for them (fabricated identity),
-- neither of which the fail-closed contract wants.
--
-- routing_policy_id / routing_candidate_hash / routing_decision_reason
-- complete the provenance 000070 started (routing_mode,
-- routing_selector_id, routing_candidate_set_hash already exist there).
-- routing_policy_id is the docs/canonical/model-routing.yaml policy key
-- (e.g. "research.worker") the route was resolved against; NULL for a
-- static route exactly like the other routing_* columns.
-- routing_candidate_hash is the SPECIFIC selected candidate's hash
-- (routing_candidates.candidate_hash), distinct from
-- routing_candidate_set_hash (the whole pool's hash at decision time).
-- routing_decision_reason is the selector's own short, non-secret
-- explanation (internal/modelrouting.Decision.Reason) -- never raw
-- capacity state, never a secret, bounded by the same discipline as
-- every other provenance column here.
ALTER TABLE model_invocations
    ADD COLUMN idempotency_intent_hash TEXT CHECK (idempotency_intent_hash IS NULL OR idempotency_intent_hash ~ '^[0-9a-f]{64}$'),
    ADD COLUMN routing_policy_id TEXT,
    ADD COLUMN routing_candidate_hash TEXT CHECK (routing_candidate_hash IS NULL OR routing_candidate_hash ~ '^[0-9a-f]{64}$'),
    ADD COLUMN routing_decision_reason TEXT CHECK (routing_decision_reason IS NULL OR length(routing_decision_reason) <= 500);
