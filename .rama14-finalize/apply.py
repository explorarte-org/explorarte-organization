from pathlib import Path

path = Path("internal/platform/postgres/integration_test.go")
text = path.read_text()

marker = "\tfor _, statement := range []string{\n\t\t`DROP TABLE IF EXISTS model_provider_outcomes`,"
replacement = """\tfor _, statement := range []string{
\t\t`DROP TRIGGER IF EXISTS decision_budget_events_immutable ON decision_budget_events`,
\t\t`DROP TRIGGER IF EXISTS decision_records_immutable ON decision_records`,
\t\t`DROP TRIGGER IF EXISTS decision_verifications_immutable ON decision_verifications`,
\t\t`DROP TRIGGER IF EXISTS decision_observations_immutable ON decision_observations`,
\t\t`DROP TRIGGER IF EXISTS decision_graph_edges_immutable ON decision_graph_edges`,
\t\t`DROP TRIGGER IF EXISTS decision_graph_versions_immutable ON decision_graph_versions`,
\t\t`DROP TRIGGER IF EXISTS decision_graph_runs_update_guard ON decision_graph_runs`,
\t\t`DROP TRIGGER IF EXISTS decision_graph_nodes_update_guard ON decision_graph_nodes`,
\t\t`DROP TRIGGER IF EXISTS decision_graph_edges_cycle_guard ON decision_graph_edges`,
\t\t`DROP TABLE IF EXISTS decision_budget_events`,
\t\t`DROP TABLE IF EXISTS decision_records`,
\t\t`DROP TABLE IF EXISTS decision_verifications`,
\t\t`DROP TABLE IF EXISTS decision_observations`,
\t\t`DROP TABLE IF EXISTS decision_node_executions`,
\t\t`DROP TABLE IF EXISTS decision_graph_budgets`,
\t\t`DROP TABLE IF EXISTS decision_graph_edges`,
\t\t`DROP TABLE IF EXISTS decision_graph_nodes`,
\t\t`DROP TABLE IF EXISTS decision_graph_versions`,
\t\t`DROP TABLE IF EXISTS decision_graph_runs`,
\t\t`DROP FUNCTION IF EXISTS decision_graph_immutable_row()`,
\t\t`DROP FUNCTION IF EXISTS decision_graph_guard_run_update()`,
\t\t`DROP FUNCTION IF EXISTS decision_graph_guard_node_update()`,
\t\t`DROP FUNCTION IF EXISTS decision_graph_reject_edge_cycle()`,
\t\t`DROP TABLE IF EXISTS model_provider_outcomes`,"""
if marker not in text:
    raise SystemExit("platform reset marker not found")
text = text.replace(marker, replacement, 1)

for old, new in {
    "len(result.Applied) != 11 || result.Current != 11": "len(result.Applied) != 12 || result.Current != 12",
    "status.Applied != 11": "status.Applied != 12",
    "len(second.Applied) != 0 || second.Current != 11": "len(second.Applied) != 0 || second.Current != 12",
    "if len(loaded) != 11": "if len(loaded) != 12",
    't.Fatalf("loaded migrations = %d, want 11", len(loaded))': 't.Fatalf("loaded migrations = %d, want 12", len(loaded))',
}.items():
    if old not in text:
        raise SystemExit(f"expected migration assertion not found: {old}")
    text = text.replace(old, new, 1)

old_tail = """\tif err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
\t\tif _, err := tx.Exec(ctx, loaded[10].DownSQL); err != nil {
\t\t\treturn err
\t\t}
\t\t_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 11`)
\t\treturn err
\t}); err != nil {
\t\tt.Fatalf("down migration 000011: %v", err)
\t}
\tfor _, table := range []string{"model_provider_requests", "model_provider_outcomes"} {
\t\tvar relation *string
\t\tif err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.' || $1)::text`, table).Scan(&relation); err != nil {
\t\t\tt.Fatalf("check down migration table %s: %v", table, err)
\t\t}
\t\tif relation != nil {
\t\t\tt.Fatalf("%s still exists after down: %v", table, *relation)
\t\t}
\t}
\trestored, err := runner.Up(ctx)
\tif err != nil {
\t\tt.Fatalf("restore migration 000011: %v", err)
\t}
\tif len(restored.Applied) != 1 || restored.Current != 11 {
\t\tt.Fatalf("restore migration result = %+v, want only 000011", restored)
\t}
"""
new_tail = """\tif err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
\t\tif _, err := tx.Exec(ctx, loaded[11].DownSQL); err != nil {
\t\t\treturn err
\t\t}
\t\t_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 12`)
\t\treturn err
\t}); err != nil {
\t\tt.Fatalf("down migration 000012: %v", err)
\t}
\tfor _, table := range []string{"decision_graph_runs", "decision_graph_versions", "decision_graph_nodes", "decision_node_executions", "decision_records"} {
\t\tvar relation *string
\t\tif err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.' || $1)::text`, table).Scan(&relation); err != nil {
\t\t\tt.Fatalf("check down migration table %s: %v", table, err)
\t\t}
\t\tif relation != nil {
\t\t\tt.Fatalf("%s still exists after down: %v", table, *relation)
\t\t}
\t}
\trestored, err := runner.Up(ctx)
\tif err != nil {
\t\tt.Fatalf("restore migration 000012: %v", err)
\t}
\tif len(restored.Applied) != 1 || restored.Current != 12 {
\t\tt.Fatalf("restore migration result = %+v, want only 000012", restored)
\t}
"""
if old_tail not in text:
    raise SystemExit("platform down/reapply block not found")
text = text.replace(old_tail, new_tail, 1)

path.write_text(text)
