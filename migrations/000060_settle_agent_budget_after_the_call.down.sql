-- NOT VALID: settlements already recorded are the true account of what calls
-- cost, and a rollback must not require deleting them. The old rule governs
-- new writes again and the history is kept.

ALTER TABLE agent_budget_events DROP CONSTRAINT agent_budget_events_kind_check;

ALTER TABLE agent_budget_events ADD CONSTRAINT agent_budget_events_kind_check
    CHECK (kind IN ('created', 'inherited', 'consumed')) NOT VALID;
