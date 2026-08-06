from pathlib import Path


def replace(path: str, old: str, new: str, count: int = 1) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old[:140]!r}")
    p.write_text(text.replace(old, new, count))


for path in [
    "internal/modelidentity/postgres/integration_test.go",
    "internal/modelegress/postgres/integration_test.go",
]:
    replace(path, "if result.Current != 11 {", "if result.Current != 12 {")
    replace(path, 't.Fatalf("current migration=%d want=11", result.Current)', 't.Fatalf("current migration=%d want=12", result.Current)')

# Context rollback must remove the decision graph before its referenced model,
# context, task, and attempt tables.
context_path = "internal/contextengine/postgres/integration_test.go"
replace(
    context_path,
    '''\t\t// Branch 12 provider tables reference identity assertions, so 000011
\t\t// must come down before 000010; 000010 must in turn come down before
\t\t// 000009 and the earlier model migrations.
\t\tproviderDown, err := rootmigrations.Files.ReadFile("000011_create_model_provider_adapter.down.sql")
''',
    '''\t\t// Capa 14 references task attempts, context snapshots, model invocations,
\t\t// and dispatch attempts. It must come down before 000011-000006.
\t\tdecisionDown, err := rootmigrations.Files.ReadFile("000012_create_durable_decision_graph.down.sql")
\t\tif err != nil {
\t\t\tt.Fatal(err)
\t\t}
\t\tproviderDown, err := rootmigrations.Files.ReadFile("000011_create_model_provider_adapter.down.sql")
''',
)
replace(
    context_path,
    '''\t\tdefer func() { _ = tx.Rollback(ctx) }()
\t\tif _, err = tx.Exec(ctx, string(providerDown)); err != nil {
''',
    '''\t\tdefer func() { _ = tx.Rollback(ctx) }()
\t\tif _, err = tx.Exec(ctx, string(decisionDown)); err != nil {
\t\t\tt.Fatalf("down migration 000012: %v", err)
\t\t}
\t\tif _, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=12`); err != nil {
\t\t\tt.Fatal(err)
\t\t}
\t\tif _, err = tx.Exec(ctx, string(providerDown)); err != nil {
''',
)
replace(
    context_path,
    "if len(result.Applied) != 6 || result.Current != 11 {",
    "if len(result.Applied) != 7 || result.Current != 12 {",
)
replace(
    context_path,
    't.Fatalf("reapply result=%+v, want migrations 000006 through 000011", result)',
    't.Fatalf("reapply result=%+v, want migrations 000006 through 000012", result)',
)

# Model runtime rollback has the same dependency chain.
model_path = "internal/modelruntime/postgres/integration_test.go"
replace(
    model_path,
    '''\t\t// Branch 12 provider tables reference identity assertions, so 000011
\t\t// must come down before 000010 and the earlier model migrations.
\t\tfor _, version := range []int{11, 10, 9, 8, 7} {
''',
    '''\t\t// Capa 14 references model invocations and dispatch attempts, so 000012
\t\t// must come down before 000011 and the earlier model migrations.
\t\tfor _, version := range []int{12, 11, 10, 9, 8, 7} {
''',
)
replace(
    model_path,
    '''for _, candidate := range []string{"000011_create_model_provider_adapter.down.sql", "000010_create_model_execution_identity.down.sql", "000009_create_model_dispatcher_assignments.down.sql", "000008_create_model_egress_authorization.down.sql", "000007_create_model_runtime_gateway.down.sql"} {''',
    '''for _, candidate := range []string{"000012_create_durable_decision_graph.down.sql", "000011_create_model_provider_adapter.down.sql", "000010_create_model_execution_identity.down.sql", "000009_create_model_dispatcher_assignments.down.sql", "000008_create_model_egress_authorization.down.sql", "000007_create_model_runtime_gateway.down.sql"} {''',
)
replace(
    model_path,
    "if upErr != nil || len(reapplied.Applied) != 5 || reapplied.Current != 11 {",
    "if upErr != nil || len(reapplied.Applied) != 6 || reapplied.Current != 12 {",
)
