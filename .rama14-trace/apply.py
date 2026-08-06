from pathlib import Path


def replace(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old[:180]!r}")
    p.write_text(text.replace(old, new, 1))


store = "internal/decisiongraph/postgres/store.go"

old_observation = '''func (s *Store) RecordObservation(ctx context.Context, record decisiongraph.ObservationRecord, now time.Time) error {
\tif err := record.Validate(); err != nil {
\t\treturn err
\t}
\tcommand, err := s.pool.Exec(ctx, `
INSERT INTO decision_observations (
    organization_id, run_id, node_id, execution_id, schema_version,
    observation_hash, source_kind, source_reference_hash, created_at
)
SELECT $1, x.run_id, x.node_id, x.id, $3, $4, $5, NULLIF($6,''), $7
FROM decision_node_executions x
WHERE x.id=$2 AND x.organization_id=$1`, s.organizationID, record.ExecutionID, record.SchemaVersion,
\t\trecord.ObservationHash, record.SourceKind, record.SourceReferenceHash, now)
\tif err != nil {
\t\treturn fmt.Errorf("insert observation: %w", err)
\t}
\tif command.RowsAffected() != 1 {
\t\treturn decisiongraph.ErrNotFound
\t}
\treturn nil
}
'''
new_observation = '''func (s *Store) RecordObservation(ctx context.Context, record decisiongraph.ObservationRecord, now time.Time) error {
\tif err := record.Validate(); err != nil {
\t\treturn err
\t}
\ttx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
\tif err != nil {
\t\treturn fmt.Errorf("begin record observation: %w", err)
\t}
\tdefer tx.Rollback(ctx)

\tvar runID, nodeID int64
\tvar runStatus decisiongraph.RunStatus
\tif err := tx.QueryRow(ctx, `
SELECT x.run_id, x.node_id, r.status
FROM decision_node_executions x
JOIN decision_graph_runs r
  ON r.id=x.run_id AND r.organization_id=x.organization_id
WHERE x.id=$1 AND x.organization_id=$2
FOR UPDATE OF x,r`, record.ExecutionID, s.organizationID).Scan(&runID, &nodeID, &runStatus); err != nil {
\t\treturn mapNotFound("observation execution", err)
\t}
\tif runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
\t\treturn decisiongraph.ErrRunNotActive
\t}
\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_observations (
    organization_id, run_id, node_id, execution_id, schema_version,
    observation_hash, source_kind, source_reference_hash, created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)`,
\t\ts.organizationID, runID, nodeID, record.ExecutionID, record.SchemaVersion,
\t\trecord.ObservationHash, record.SourceKind, record.SourceReferenceHash, now); err != nil {
\t\treturn fmt.Errorf("insert observation: %w", err)
\t}
\tif err := tx.Commit(ctx); err != nil {
\t\treturn fmt.Errorf("commit observation: %w", err)
\t}
\treturn nil
}
'''
replace(store, old_observation, new_observation)

replace(
    store,
    '''\tvar maxVerifications, usedVerifications int64
\tif err := tx.QueryRow(ctx, `
SELECT r.max_verifications, b.used_verifications
FROM decision_graph_runs r
JOIN decision_graph_budgets b
  ON b.run_id=r.id AND b.organization_id=r.organization_id
WHERE r.id=$1 AND r.organization_id=$2
FOR UPDATE OF r,b`, record.RunID, s.organizationID).Scan(&maxVerifications, &usedVerifications); err != nil {
\t\treturn mapNotFound("run", err)
\t}
''',
    '''\tvar runStatus decisiongraph.RunStatus
\tvar maxVerifications, usedVerifications int64
\tif err := tx.QueryRow(ctx, `
SELECT r.status, r.max_verifications, b.used_verifications
FROM decision_graph_runs r
JOIN decision_graph_budgets b
  ON b.run_id=r.id AND b.organization_id=r.organization_id
WHERE r.id=$1 AND r.organization_id=$2
FOR UPDATE OF r,b`, record.RunID, s.organizationID).Scan(&runStatus, &maxVerifications, &usedVerifications); err != nil {
\t\treturn mapNotFound("run", err)
\t}
\tif runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
\t\treturn decisiongraph.ErrRunNotActive
\t}
''',
)

replace(
    store,
    '''\tif supportingVerifications == 0 {
\t\treturn decisiongraph.ErrInvalidDecision
\t}
\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_records (
''',
    '''\tif supportingVerifications == 0 {
\t\treturn decisiongraph.ErrInvalidDecision
\t}
\tvar activeExecutions int
\tif err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM decision_node_executions
WHERE run_id=$1 AND organization_id=$2
  AND status IN ('claimed','running','waiting_verification')`, request.RunID, s.organizationID).Scan(&activeExecutions); err != nil {
\t\treturn fmt.Errorf("count active decision executions: %w", err)
\t}
\tif activeExecutions != 0 {
\t\treturn decisiongraph.ErrRunNotMutable
\t}
\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_records (
''',
)

old_trace = '''func (s *Store) TraceRef(ctx context.Context, runID int64) (decisiongraph.TraceRef, error) {
\tvar policyHash, graphHash, decisionHash string
\tif err := s.pool.QueryRow(ctx, `
SELECT r.reasoning_policy_hash, v.snapshot_hash, d.decision_hash
FROM decision_graph_runs r
JOIN decision_graph_versions v ON v.run_id=r.id
JOIN decision_records d ON d.run_id=r.id AND d.graph_version_id=v.id
WHERE r.id=$1 AND r.organization_id=$2
ORDER BY v.version_number DESC
LIMIT 1`, runID, s.organizationID).Scan(&policyHash, &graphHash, &decisionHash); err != nil {
\t\treturn decisiongraph.TraceRef{}, mapNotFound("trace", err)
\t}
\ttraceHash := eventDigest("decision-trace/v1", runID, policyHash, graphHash, decisionHash)
\treturn decisiongraph.TraceRef{
\t\tOrganizationID: s.organizationID,
\t\tRunID:          runID,
\t\tTraceHash:      traceHash,
\t\tSchemaVersion:  "decision-trace/v1",
\t}, nil
}
'''
new_trace = '''func (s *Store) TraceRef(ctx context.Context, runID int64) (decisiongraph.TraceRef, error) {
\tvar policyHash, graphHash, decisionHash string
\tvar executionTrace, observationTrace, verificationTrace, branchTrace, budgetTrace string
\tif err := s.pool.QueryRow(ctx, `
SELECT
    r.reasoning_policy_hash,
    v.snapshot_hash,
    d.decision_hash,
    COALESCE((
        SELECT string_agg(
            concat_ws(':', x.id, x.node_id, x.attempt_number, x.status,
                COALESCE(x.outcome_hash,''), COALESCE(x.reason_code,''),
                x.input_tokens, x.output_tokens,
                COALESCE(x.model_invocation_id::text,''), COALESCE(x.dispatch_attempt_id::text,'')),
            '|' ORDER BY x.id)
        FROM decision_node_executions x
        WHERE x.run_id=r.id AND x.organization_id=r.organization_id
    ),''),
    COALESCE((
        SELECT string_agg(
            concat_ws(':', o.id, o.execution_id, o.schema_version,
                o.observation_hash, o.source_kind, COALESCE(o.source_reference_hash,'')),
            '|' ORDER BY o.id)
        FROM decision_observations o
        WHERE o.run_id=r.id AND o.organization_id=r.organization_id
    ),''),
    COALESCE((
        SELECT string_agg(
            concat_ws(':', q.id, q.node_id, COALESCE(q.execution_id::text,''),
                q.label, q.verifier_ref, q.verifier_version,
                q.evidence_set_hash, q.reason_codes::text),
            '|' ORDER BY q.id)
        FROM decision_verifications q
        WHERE q.run_id=r.id AND q.organization_id=r.organization_id
    ),''),
    COALESCE((
        SELECT string_agg(e.event_hash, '|' ORDER BY e.id)
        FROM decision_branch_events e
        WHERE e.run_id=r.id AND e.organization_id=r.organization_id
    ),''),
    COALESCE((
        SELECT string_agg(e.event_hash, '|' ORDER BY e.id)
        FROM decision_budget_events e
        WHERE e.run_id=r.id AND e.organization_id=r.organization_id
    ),'')
FROM decision_graph_runs r
JOIN decision_graph_versions v ON v.run_id=r.id AND v.organization_id=r.organization_id
JOIN decision_records d ON d.run_id=r.id AND d.graph_version_id=v.id AND d.organization_id=r.organization_id
WHERE r.id=$1 AND r.organization_id=$2 AND r.status='succeeded'
ORDER BY v.version_number DESC
LIMIT 1`, runID, s.organizationID).Scan(
\t\t&policyHash, &graphHash, &decisionHash,
\t\t&executionTrace, &observationTrace, &verificationTrace, &branchTrace, &budgetTrace,
\t); err != nil {
\t\treturn decisiongraph.TraceRef{}, mapNotFound("trace", err)
\t}
\ttraceHash := eventDigest(
\t\t"decision-trace/v1", runID, policyHash, graphHash, decisionHash,
\t\texecutionTrace, observationTrace, verificationTrace, branchTrace, budgetTrace,
\t)
\treturn decisiongraph.TraceRef{
\t\tOrganizationID: s.organizationID,
\t\tRunID:          runID,
\t\tTraceHash:      traceHash,
\t\tSchemaVersion:  "decision-trace/v1",
\t}, nil
}
'''
replace(store, old_trace, new_trace)

# Integration proves the successful trace becomes immutable at run terminal.
test = "internal/decisiongraph/postgres/integration_test.go"
replace(
    test,
    '''\tif trace.OrganizationID != decisionGraphOrganization || trace.RunID != run.ID || trace.SchemaVersion != "decision-trace/v1" || len(trace.TraceHash) != 64 {
\t\tt.Fatalf("trace=%+v", trace)
\t}
\tassertTerminalDecisionImmutable(t, ctx, platform, run.ID)
''',
    '''\tif trace.OrganizationID != decisionGraphOrganization || trace.RunID != run.ID || trace.SchemaVersion != "decision-trace/v1" || len(trace.TraceHash) != 64 {
\t\tt.Fatalf("trace=%+v", trace)
\t}
\tif err := service.RecordObservation(ctx, decisiongraph.ObservationRecord{
\t\tExecutionID: decision.ExecutionID, SchemaVersion: "observation/v1",
\t\tObservationHash: digest("late-observation"), SourceKind: "model_result",
\t}); !errors.Is(err, decisiongraph.ErrRunNotActive) {
\t\tt.Fatalf("late observation error=%v, want run not active", err)
\t}
\tif err := service.RecordVerification(ctx, decisiongraph.VerificationRecord{
\t\tRunID: run.ID, NodeID: candidate.NodeID, Label: decisiongraph.VerificationVerified,
\t\tVerifierRef: "integration/late-verifier", VerifierVersion: "v1",
\t\tEvidenceSetHash: digest("late-evidence"),
\t}); !errors.Is(err, decisiongraph.ErrRunNotActive) {
\t\tt.Fatalf("late verification error=%v, want run not active", err)
\t}
\tstableTrace, err := service.TraceRef(ctx, run.ID)
\tif err != nil {
\t\tt.Fatal(err)
\t}
\tif stableTrace.TraceHash != trace.TraceHash {
\t\tt.Fatalf("terminal trace mutated: %s != %s", stableTrace.TraceHash, trace.TraceHash)
\t}
\tassertTerminalDecisionImmutable(t, ctx, platform, run.ID)
''',
)

fitness = "scripts/check-decisiongraph-fitness.sh"
replace(
    fitness,
    '''rg -q 'decision_branch_events' migrations/000012_create_durable_decision_graph.up.sql internal/decisiongraph/postgres/store.go || fail "branch transition ledger missing"
''',
    '''rg -q 'decision_branch_events' migrations/000012_create_durable_decision_graph.up.sql internal/decisiongraph/postgres/store.go || fail "branch transition ledger missing"
rg -q 'string_agg' internal/decisiongraph/postgres/store.go || fail "TraceRef does not commit to the structured trace ledger"
rg -q "r.status='succeeded'" internal/decisiongraph/postgres/store.go || fail "TraceRef is not restricted to terminal successful runs"
''',
)
