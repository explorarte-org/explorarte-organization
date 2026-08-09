DROP TRIGGER IF EXISTS agent_budget_events_no_mutation ON agent_budget_events;
DROP FUNCTION IF EXISTS reject_agent_budget_event_mutation();
DROP TABLE IF EXISTS agent_budget_events;
DROP TABLE IF EXISTS agent_budgets;
