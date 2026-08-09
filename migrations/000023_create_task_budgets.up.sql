-- Maps every task in a budget tree — root or child, whether it shares its
-- parent's budget row or has its own carved-out allocation — to the
-- budget row it should actually consume against. Populated by
-- CreateRootBudget and InheritForChild, read by the dispatch path to
-- resolve "which budget does this task's model call consume."
CREATE TABLE task_budgets (
    task_id BIGINT PRIMARY KEY REFERENCES tasks(id) ON DELETE RESTRICT,
    budget_id BIGINT NOT NULL REFERENCES agent_budgets(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX task_budgets_budget_idx ON task_budgets (budget_id);
