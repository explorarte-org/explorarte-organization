from pathlib import Path

path = Path("internal/platform/postgres/integration_test.go")
text = path.read_text()
text = text.replace(
    "\t\t`DROP TRIGGER IF EXISTS decision_graph_edges_immutable ON decision_graph_edges`,\n",
    "\t\t`DROP TRIGGER IF EXISTS decision_graph_edges_immutable ON decision_graph_edges`,\n\t\t`DROP TRIGGER IF EXISTS decision_branch_events_immutable ON decision_branch_events`,\n",
    1,
)
text = text.replace(
    "\t\t`DROP TABLE IF EXISTS decision_graph_budgets`,\n\t\t`DROP TABLE IF EXISTS decision_graph_edges`,\n",
    "\t\t`DROP TABLE IF EXISTS decision_graph_budgets`,\n\t\t`DROP TABLE IF EXISTS decision_branch_events`,\n\t\t`DROP TABLE IF EXISTS decision_graph_edges`,\n",
    1,
)
if "decision_branch_events_immutable" not in text or "DROP TABLE IF EXISTS decision_branch_events" not in text:
    raise SystemExit("decision branch reset update did not apply")
path.write_text(text)
