from pathlib import Path


def replace(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old[:140]!r}")
    p.write_text(text.replace(old, new, 1))


store = "internal/decisiongraph/postgres/store.go"

replace(store, "\tvar expiresAt time.Time\n", "\tvar claimedAt, expiresAt time.Time\n")
replace(
    store,
    '''SELECT run_id, node_id, status, claim_token_hash, claim_expires_at
FROM decision_node_executions
WHERE id=$1 AND organization_id=$2
FOR UPDATE`, request.ExecutionID, s.organizationID).Scan(&runID, &nodeID, &status, &tokenHash, &expiresAt); err != nil {''',
    '''SELECT run_id, node_id, status, claim_token_hash, claimed_at, claim_expires_at
FROM decision_node_executions
WHERE id=$1 AND organization_id=$2
FOR UPDATE`, request.ExecutionID, s.organizationID).Scan(&runID, &nodeID, &status, &tokenHash, &claimedAt, &expiresAt); err != nil {''',
)
replace(
    store,
    '''\tvar maxInput, maxOutput int64
\tif err := tx.QueryRow(ctx, `SELECT max_input_tokens, max_output_tokens FROM decision_graph_runs WHERE id=$1 AND organization_id=$2 FOR UPDATE`, runID, s.organizationID).Scan(&maxInput, &maxOutput); err != nil {
\t\treturn fmt.Errorf("lock run token budget: %w", err)
\t}
\tvar usedInput, usedOutput, active int64
\tif err := tx.QueryRow(ctx, `
SELECT used_input_tokens, used_output_tokens, active_parallel_nodes
FROM decision_graph_budgets
WHERE run_id=$1 AND organization_id=$2
FOR UPDATE`, runID, s.organizationID).Scan(&usedInput, &usedOutput, &active); err != nil {
\t\treturn fmt.Errorf("lock finish budget: %w", err)
\t}
\tif active <= 0 {
\t\treturn decisiongraph.ErrInvalidBudget
\t}
\toverBudget := usedInput+request.InputTokens > maxInput || usedOutput+request.OutputTokens > maxOutput
''',
    '''\tvar maxInput, maxOutput, maxWallTimeMS int64
\tif err := tx.QueryRow(ctx, `SELECT max_input_tokens, max_output_tokens, max_wall_time_ms FROM decision_graph_runs WHERE id=$1 AND organization_id=$2 FOR UPDATE`, runID, s.organizationID).Scan(&maxInput, &maxOutput, &maxWallTimeMS); err != nil {
\t\treturn fmt.Errorf("lock run execution budget: %w", err)
\t}
\tvar usedInput, usedOutput, usedWallTimeMS, active int64
\tif err := tx.QueryRow(ctx, `
SELECT used_input_tokens, used_output_tokens, used_wall_time_ms, active_parallel_nodes
FROM decision_graph_budgets
WHERE run_id=$1 AND organization_id=$2
FOR UPDATE`, runID, s.organizationID).Scan(&usedInput, &usedOutput, &usedWallTimeMS, &active); err != nil {
\t\treturn fmt.Errorf("lock finish budget: %w", err)
\t}
\tif active <= 0 {
\t\treturn decisiongraph.ErrInvalidBudget
\t}
\telapsedMS := elapsedMilliseconds(claimedAt, now)
\toverBudget := usedInput+request.InputTokens > maxInput ||
\t\tusedOutput+request.OutputTokens > maxOutput ||
\t\tusedWallTimeMS+elapsedMS > maxWallTimeMS
''',
)
replace(
    store,
    '''UPDATE decision_graph_budgets
SET active_parallel_nodes=active_parallel_nodes-1,
    used_input_tokens=used_input_tokens+$3,
    used_output_tokens=used_output_tokens+$4,
    version=version+1,
    updated_at=$5
WHERE run_id=$1 AND organization_id=$2`, runID, s.organizationID, request.InputTokens, request.OutputTokens, now); err != nil {''',
    '''UPDATE decision_graph_budgets
SET active_parallel_nodes=active_parallel_nodes-1,
    used_input_tokens=used_input_tokens+$3,
    used_output_tokens=used_output_tokens+$4,
    used_wall_time_ms=used_wall_time_ms+$5,
    version=version+1,
    updated_at=$6
WHERE run_id=$1 AND organization_id=$2`, runID, s.organizationID, request.InputTokens, request.OutputTokens, elapsedMS, now); err != nil {''',
)
replace(
    store,
    '''\t\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='rejected_by_budget', updated_at=$2
WHERE id=$1 AND branch_state='active'`, nodeID, now); err != nil {
\t\t\treturn fmt.Errorf("reject over-budget branch: %w", err)
\t\t}
''',
    '''\t\tbranchEventHash := eventDigest("branch_transitioned", runID, nodeID, decisiongraph.BranchActive, decisiongraph.BranchRejectedByBudget, "", "budget_exceeded", "decisiongraph/budget")
\t\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    reason_code,actor,event_hash,created_at
)
SELECT organization_id,run_id,graph_version_id,id,'active','rejected_by_budget',
       'budget_exceeded','decisiongraph/budget',$3,$4
FROM decision_graph_nodes
WHERE id=$1 AND run_id=$2 AND branch_state='active'`, nodeID, runID, branchEventHash, now); err != nil {
\t\t\treturn fmt.Errorf("record over-budget branch event: %w", err)
\t\t}
\t\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='rejected_by_budget', updated_at=$2
WHERE id=$1 AND branch_state='active'`, nodeID, now); err != nil {
\t\t\treturn fmt.Errorf("reject over-budget branch: %w", err)
\t\t}
''',
)
replace(
    store,
    '''INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta,
    input_tokens_delta, output_tokens_delta, event_hash, created_at
) VALUES ($1,$2,'execution_finished',-1,$3,$4,$5,$6)`, s.organizationID, runID, request.InputTokens, request.OutputTokens, eventHash, now); err != nil {''',
    '''INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta,
    input_tokens_delta, output_tokens_delta, wall_time_delta_ms, event_hash, created_at
) VALUES ($1,$2,'execution_finished',-1,$3,$4,$5,$6,$7)`, s.organizationID, runID, request.InputTokens, request.OutputTokens, elapsedMS, eventHash, now); err != nil {''',
)

# Recovery records elapsed wall time only for claims that had not already
# released their parallel slot at waiting_verification.
replace(
    store,
    '''SELECT x.id, x.run_id, x.node_id, x.status, x.model_invocation_id, mi.status
FROM decision_node_executions x''',
    '''SELECT x.id, x.run_id, x.node_id, x.status, x.claimed_at, x.model_invocation_id, mi.status
FROM decision_node_executions x''',
)
replace(
    store,
    '''\t\tstatus           string
\t\tinvocationID     *int64
''',
    '''\t\tstatus           string
\t\tclaimedAt        time.Time
\t\tinvocationID     *int64
''',
)
replace(
    store,
    '''if err := rows.Scan(&item.id, &item.runID, &item.nodeID, &item.status, &item.invocationID, &item.invocationStatus); err != nil {''',
    '''if err := rows.Scan(&item.id, &item.runID, &item.nodeID, &item.status, &item.claimedAt, &item.invocationID, &item.invocationStatus); err != nil {''',
)
replace(
    store,
    '''\t\tparallelDelta := 0
\t\tif item.status == "claimed" || item.status == "running" {
\t\t\tparallelDelta = -1
\t\t\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_budgets
SET active_parallel_nodes=active_parallel_nodes-1,
    version=version+1,
    updated_at=$3
WHERE run_id=$1 AND organization_id=$2
  AND active_parallel_nodes>0`, item.runID, s.organizationID, now); err != nil {
\t\t\t\treturn 0, fmt.Errorf("release recovered parallel budget: %w", err)
\t\t\t}
\t\t}
''',
    '''\t\tparallelDelta := 0
\t\twallTimeDeltaMS := int64(0)
\t\toverWallBudget := false
\t\tif item.status == "claimed" || item.status == "running" {
\t\t\tparallelDelta = -1
\t\t\twallTimeDeltaMS = elapsedMilliseconds(item.claimedAt, now)
\t\t\tvar usedWallTimeMS, maxWallTimeMS int64
\t\t\tif err := tx.QueryRow(ctx, `
UPDATE decision_graph_budgets b
SET active_parallel_nodes=active_parallel_nodes-1,
    used_wall_time_ms=used_wall_time_ms+$3,
    version=version+1,
    updated_at=$4
FROM decision_graph_runs r
WHERE b.run_id=$1 AND b.organization_id=$2
  AND r.id=b.run_id AND r.organization_id=b.organization_id
  AND b.active_parallel_nodes>0
RETURNING b.used_wall_time_ms,r.max_wall_time_ms`, item.runID, s.organizationID, wallTimeDeltaMS, now).Scan(&usedWallTimeMS, &maxWallTimeMS); err != nil {
\t\t\t\treturn 0, fmt.Errorf("release recovered execution budget: %w", err)
\t\t\t}
\t\t\toverWallBudget = usedWallTimeMS > maxWallTimeMS
\t\t}
''',
)
replace(
    store,
    '''\t\tif finalState == decisiongraph.ExecutionAmbiguous {
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
\t\t}
\t\tif finalState == decisiongraph.ExecutionAmbiguous {
''',
)
replace(
    store,
    '''INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta, event_hash, created_at
) VALUES ($1,$2,'execution_finished',$3,$4,$5)`, s.organizationID, item.runID, parallelDelta, eventHash, now); err != nil {''',
    '''INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta, wall_time_delta_ms, event_hash, created_at
) VALUES ($1,$2,'execution_finished',$3,$4,$5,$6)`, s.organizationID, item.runID, parallelDelta, wallTimeDeltaMS, eventHash, now); err != nil {''',
)

# Shared duration helper rounds partial milliseconds upward so short executions
# cannot disappear from the durable budget.
replace(
    store,
    "func eventDigest(kind string, runID int64, values ...any) string {\n",
    '''func elapsedMilliseconds(start, end time.Time) int64 {
\tif !end.After(start) {
\t\treturn 0
\t}
\tduration := end.Sub(start)
\tmilliseconds := duration.Milliseconds()
\tif duration%time.Millisecond != 0 {
\t\tmilliseconds++
\t}
\treturn milliseconds
}

func eventDigest(kind string, runID int64, values ...any) string {
''',
)

# Integration verifies normal consumption and hard budget failure.
test = "internal/decisiongraph/postgres/integration_test.go"
replace(
    test,
    '''\tif err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
\t\tExecutionID:  candidate.ExecutionID,
''',
    '''\tclock.Set(now.Add(1500 * time.Millisecond))
\tif err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
\t\tExecutionID:  candidate.ExecutionID,
''',
)
replace(
    test,
    '''\tif err := service.RecordObservation(ctx, decisiongraph.ObservationRecord{
''',
    '''\tvar usedWallTimeMS int64
\tif err := platform.Pool().QueryRow(ctx, `SELECT used_wall_time_ms FROM decision_graph_budgets WHERE run_id=$1`, run.ID).Scan(&usedWallTimeMS); err != nil {
\t\tt.Fatal(err)
\t}
\tif usedWallTimeMS != 1500 {
\t\tt.Fatalf("used wall time=%dms, want 1500ms", usedWallTimeMS)
\t}
\tif err := service.RecordObservation(ctx, decisiongraph.ObservationRecord{
''',
)
replace(
    test,
    '''\tt.Run("concurrent claim has one winner", func(t *testing.T) {
''',
    '''\tt.Run("wall-time budget fails the run atomically", func(t *testing.T) {
\t\ttinyLimits := limits
\t\ttinyLimits.MaxWallTime = time.Millisecond
\t\twallRun := createSimpleRun(t, ctx, service, taskID, attemptID, tinyLimits, "wall-budget", clock.Now())
\t\tclaim, claimErr := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
\t\t\tRunID: wallRun.ID, ClaimedBy: "integration/wall-budget", LeaseDuration: time.Minute,
\t\t})
\t\tif claimErr != nil {
\t\t\tt.Fatal(claimErr)
\t\t}
\t\tclock.Set(clock.Now().Add(2 * time.Millisecond))
\t\tfinishErr := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
\t\t\tExecutionID: claim.ExecutionID, ClaimToken: claim.ClaimToken,
\t\t\tFinalState: decisiongraph.ExecutionSucceeded,
\t\t})
\t\tif !errors.Is(finishErr, decisiongraph.ErrBudgetExceeded) {
\t\t\tt.Fatalf("finish error=%v, want budget exceeded", finishErr)
\t\t}
\t\tvar status decisiongraph.RunStatus
\t\tif err := platform.Pool().QueryRow(ctx, `SELECT status FROM decision_graph_runs WHERE id=$1`, wallRun.ID).Scan(&status); err != nil {
\t\t\tt.Fatal(err)
\t\t}
\t\tif status != decisiongraph.RunFailed {
\t\t\tt.Fatalf("run status=%s, want failed", status)
\t\t}
\t})

\tt.Run("concurrent claim has one winner", func(t *testing.T) {
''',
)

# Fitness enforces wall-time consumption.
replace(
    "scripts/check-decisiongraph-fitness.sh",
    "rg -q 'decision_budget_events' \"$store\" || fail \"append-only budget event ledger missing\"\n",
    "rg -q 'decision_budget_events' \"$store\" || fail \"append-only budget event ledger missing\"\nrg -q 'used_wall_time_ms=used_wall_time_ms' \"$store\" || fail \"wall-time budget is not consumed\"\nrg -q 'wall_time_delta_ms' \"$store\" || fail \"wall-time budget events are missing\"\n",
)
