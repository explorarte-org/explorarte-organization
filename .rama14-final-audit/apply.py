from pathlib import Path


def replace(path: str, old: str, new: str, count: int = 1) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old[:160]!r}")
    p.write_text(text.replace(old, new, count))


# Branch events are an append-only history, not an idempotency table. The same
# transition content can legitimately recur after evidence reopens a branch.
migration = "migrations/000012_create_durable_decision_graph.up.sql"
replace(
    migration,
    '''    UNIQUE (run_id, event_hash),
    CHECK (from_branch_state <> to_branch_state),
''',
    '''    CHECK (from_branch_state <> to_branch_state),
''',
)

# Avoid signed overflow before PostgreSQL records actual over-budget usage.
store = "internal/decisiongraph/postgres/store.go"
replace(
    store,
    '''\toverBudget := usedInput+request.InputTokens > maxInput ||
\t\tusedOutput+request.OutputTokens > maxOutput ||
\t\tusedWallTimeMS+elapsedMS > maxWallTimeMS
''',
    '''\toverBudget := usedInput > maxInput || request.InputTokens > maxInput-usedInput ||
\t\tusedOutput > maxOutput || request.OutputTokens > maxOutput-usedOutput ||
\t\tusedWallTimeMS > maxWallTimeMS || elapsedMS > maxWallTimeMS-usedWallTimeMS
''',
)

# Recovery must reject the affected branch when wall-time exhaustion fails the
# run, matching the normal FinishExecution path.
replace(
    store,
    '''\t\tif overWallBudget && finalState != decisiongraph.ExecutionAmbiguous {
\t\t\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_runs
SET status='failed', terminal_at=$3,
    terminal_reason_code='budget_exceeded', updated_at=$3
WHERE id=$1 AND organization_id=$2
  AND status IN ('planned','running','waiting')`, item.runID, s.organizationID, now); err != nil {
\t\t\t\treturn 0, fmt.Errorf("fail recovered over-budget run: %w", err)
\t\t\t}
\t\t}
''',
    '''\t\tif overWallBudget && finalState != decisiongraph.ExecutionAmbiguous {
\t\t\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_runs
SET status='failed', terminal_at=$3,
    terminal_reason_code='budget_exceeded', updated_at=$3
WHERE id=$1 AND organization_id=$2
  AND status IN ('planned','running','waiting')`, item.runID, s.organizationID, now); err != nil {
\t\t\t\treturn 0, fmt.Errorf("fail recovered over-budget run: %w", err)
\t\t\t}
\t\t\tbranchEventHash := eventDigest("branch_transitioned", item.runID, item.nodeID, decisiongraph.BranchActive, decisiongraph.BranchRejectedByBudget, "", "budget_exceeded", "decisiongraph/recovery", now.UTC().Format(time.RFC3339Nano))
\t\t\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    reason_code,actor,event_hash,created_at
)
SELECT organization_id,run_id,graph_version_id,id,'active','rejected_by_budget',
       'budget_exceeded','decisiongraph/recovery',$3,$4
FROM decision_graph_nodes
WHERE id=$1 AND run_id=$2 AND branch_state='active'`, item.nodeID, item.runID, branchEventHash, now); err != nil {
\t\t\t\treturn 0, fmt.Errorf("record recovered over-budget branch event: %w", err)
\t\t\t}
\t\t\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='rejected_by_budget', updated_at=$2
WHERE id=$1 AND branch_state='active'`, item.nodeID, now); err != nil {
\t\t\t\treturn 0, fmt.Errorf("reject recovered over-budget branch: %w", err)
\t\t\t}
\t\t}
''',
)

# Strict JSON means exactly one top-level value, not merely no unknown fields.
cli = "cmd/orgctl/decision.go"
replace(
    cli,
    '''\tif decoder.More() {
\t\tfmt.Fprintln(stderr, "decode decision request: multiple JSON values")
\t\treturn false, exitUsage
\t}
''',
    '''\tvar trailing any
\tif err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
\t\tfmt.Fprintln(stderr, "decode decision request: multiple JSON values")
\t\treturn false, exitUsage
\t}
''',
)
replace(
    cli,
    '''\tcase errors.Is(err, decisiongraph.ErrInvalidRun),
''',
    '''\tcase errors.Is(err, decisiongraph.ErrIdempotencyConflict),
\t\terrors.Is(err, decisiongraph.ErrInvalidRun),
''',
)
replace(
    cli,
    '''\tcase errors.Is(err, decisiongraph.ErrBudgetExceeded),
\t\terrors.Is(err, decisiongraph.ErrRunNotActive),
''',
    '''\tcase errors.Is(err, decisiongraph.ErrBudgetExceeded),
\t\terrors.Is(err, decisiongraph.ErrRunNotMutable),
\t\terrors.Is(err, decisiongraph.ErrRunNotActive),
\t\terrors.Is(err, decisiongraph.ErrRunDeadlineExceeded),
''',
)

# The compatibility alias is no longer used after the durable store landed.
hashing = "internal/decisiongraph/hashing.go"
p = Path(hashing)
text = p.read_text()
alias = '''
// HashGraph preserves the original package-level API used by the durable
// PostgreSQL ledger bundle while delegating to the canonical method. New code
// should prefer (*Graph).CanonicalHash directly.
func HashGraph(graph *Graph) (string, error) {
\treturn graph.CanonicalHash()
}
'''
if alias not in text:
    raise SystemExit("HashGraph compatibility alias not found")
p.write_text(text.replace(alias, "", 1))

# CLI test proves a second top-level JSON value is rejected.
test = "cmd/orgctl/decision_test.go"
replace(
    test,
    '''func TestParseDecisionFileAcceptsStrictInput(t *testing.T) {
''',
    '''func TestParseDecisionFileRejectsMultipleTopLevelValues(t *testing.T) {
\tpath := writeDecisionInput(t, `{"run_id":1,"claimed_by":"worker","lease_duration":"1m"} {"run_id":2}`)
\tvar input decisionClaimInput
\tvar stderr bytes.Buffer
\tif _, code := parseDecisionFile([]string{"--file", path}, &stderr, &input); code != exitUsage {
\t\tt.Fatalf("code=%d, want %d", code, exitUsage)
\t}
}

func TestParseDecisionFileAcceptsStrictInput(t *testing.T) {
''',
)
