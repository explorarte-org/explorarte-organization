-- AUTONOMY-SMOKE-017-R4 exhausted a 1.5M token campaign ceiling without
-- reaching EngineeringMission. The measured breakdown:
--
--   output headroom reserved   1,024,000   (8 calls x 128,000)
--   input estimated              462,067
--   total charged              1,486,067
--   output actually produced        36,753
--
-- The budget is charged once, BEFORE the call, with the estimated input plus
-- the MAXIMUM output the call is allowed to emit, and nothing ever corrects
-- it. Gate.Reconcile trues up the USD wallet and never touches the agent
-- budget, so 69% of the campaign was spent on output space that was never
-- used -- a factor of 28 between what was reserved and what was emitted.
--
-- A reservation that is never settled is not a budget, it is a toll. This
-- adds the settlement event so a call can be charged for what it did.
--
-- idempotency_ref means invocation_id for 'reconciled', as it does for
-- 'consumed', and the existing unique (budget_id, kind, idempotency_ref)
-- makes a repeated settlement apply exactly once. The deltas stay signed
-- because a settlement is usually a refund.

ALTER TABLE agent_budget_events DROP CONSTRAINT agent_budget_events_kind_check;

ALTER TABLE agent_budget_events ADD CONSTRAINT agent_budget_events_kind_check
    CHECK (kind IN ('created', 'inherited', 'consumed', 'reconciled'));
