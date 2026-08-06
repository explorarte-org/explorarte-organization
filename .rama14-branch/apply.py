from pathlib import Path


def replace(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old[:120]!r}")
    p.write_text(text.replace(old, new, 1))


# Domain request and validation.
replace(
    "internal/decisiongraph/records.go",
    "type TraceRef struct {\n",
    '''type BranchTransitionRequest struct {
\tRunID        int64
\tNodeID       int64
\tToState      BranchState
\tEvidenceHash string
\tReasonCode   string
\tActor        string
}

func (r BranchTransitionRequest) Validate() error {
\tif r.RunID <= 0 || r.NodeID <= 0 || !r.ToState.Valid() {
\t\treturn fmt.Errorf("%w: invalid branch transition identity", ErrInvalidTransition)
\t}
\tif r.ToState == BranchActive && !sha256HexPattern.MatchString(r.EvidenceHash) {
\t\treturn fmt.Errorf("%w: reopening requires an evidence hash", ErrInvalidTransition)
\t}
\tif r.EvidenceHash != "" && !sha256HexPattern.MatchString(r.EvidenceHash) {
\t\treturn fmt.Errorf("%w: invalid evidence hash", ErrInvalidTransition)
\t}
\tif r.ReasonCode != "" && !regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,119}$`).MatchString(r.ReasonCode) {
\t\treturn fmt.Errorf("%w: invalid branch reason code", ErrInvalidTransition)
\t}
\tif strings.TrimSpace(r.Actor) == "" || len(r.Actor) > 200 {
\t\treturn fmt.Errorf("%w: invalid branch actor", ErrInvalidTransition)
\t}
\treturn nil
}

type TraceRef struct {
''',
)

# Port and service method.
replace(
    "internal/decisiongraph/ports.go",
    "\tStartRun(context.Context, int64, time.Time) error\n",
    "\tStartRun(context.Context, int64, time.Time) error\n\tTransitionBranch(context.Context, BranchTransitionRequest, time.Time) error\n",
)
replace(
    "internal/decisiongraph/service.go",
    "func (s *Service) ClaimReadyNode(ctx context.Context, request ClaimNodeRequest) (NodeClaim, error) {\n",
    '''func (s *Service) TransitionBranch(ctx context.Context, request BranchTransitionRequest) error {
\tif err := request.Validate(); err != nil {
\t\treturn err
\t}
\tif err := s.ledger.TransitionBranch(ctx, request, postgresTimestamp(s.clock.Now())); err != nil {
\t\treturn fmt.Errorf("transition decision branch: %w", err)
\t}
\treturn nil
}

func (s *Service) ClaimReadyNode(ctx context.Context, request ClaimNodeRequest) (NodeClaim, error) {
''',
)

# Fake ledger and unit coverage.
replace(
    "internal/decisiongraph/service_test.go",
    "func (*fakeLedger) StartRun(context.Context, int64, time.Time) error { return nil }\n",
    "func (*fakeLedger) StartRun(context.Context, int64, time.Time) error { return nil }\nfunc (*fakeLedger) TransitionBranch(context.Context, BranchTransitionRequest, time.Time) error { return nil }\n",
)
replace(
    "internal/decisiongraph/service_test.go",
    "func TestServiceBoundsRecoveryBatch(t *testing.T) {\n",
    '''func TestServiceRejectsReopenWithoutEvidence(t *testing.T) {
\tledger := &fakeLedger{}
\tservice, err := NewService(ledger, fixedClock{now: time.Unix(1000, 0).UTC()})
\tif err != nil {
\t\tt.Fatal(err)
\t}
\terr = service.TransitionBranch(context.Background(), BranchTransitionRequest{
\t\tRunID: 1, NodeID: 2, ToState: BranchActive, Actor: "planner",
\t})
\tif !errors.Is(err, ErrInvalidTransition) {
\t\tt.Fatalf("expected invalid evidence-less reopen, got %v", err)
\t}
}

func TestServiceBoundsRecoveryBatch(t *testing.T) {
''',
)

# PostgreSQL store operation.
replace(
    "internal/decisiongraph/postgres/store.go",
    "func (s *Store) ClaimReadyNode(ctx context.Context, request decisiongraph.ClaimNodeRequest, now time.Time) (decisiongraph.NodeClaim, error) {\n",
    '''func (s *Store) TransitionBranch(ctx context.Context, request decisiongraph.BranchTransitionRequest, now time.Time) error {
\tif err := request.Validate(); err != nil {
\t\treturn err
\t}
\ttx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
\tif err != nil {
\t\treturn fmt.Errorf("begin branch transition: %w", err)
\t}
\tdefer tx.Rollback(ctx)

\tvar runStatus decisiongraph.RunStatus
\tvar graphVersionID int64
\tvar current decisiongraph.BranchState
\tif err := tx.QueryRow(ctx, `
SELECT r.status, n.graph_version_id, n.branch_state
FROM decision_graph_runs r
JOIN decision_graph_nodes n
  ON n.run_id=r.id AND n.organization_id=r.organization_id
WHERE r.id=$1 AND r.organization_id=$2 AND n.id=$3
  AND n.graph_version_id=(
      SELECT id FROM decision_graph_versions
      WHERE run_id=r.id AND organization_id=r.organization_id
      ORDER BY version_number DESC LIMIT 1
  )
FOR UPDATE OF r,n`, request.RunID, s.organizationID, request.NodeID).Scan(&runStatus, &graphVersionID, &current); err != nil {
\t\treturn mapNotFound("branch node", err)
\t}
\tif runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
\t\treturn decisiongraph.ErrRunNotActive
\t}
\treopened := request.ToState == decisiongraph.BranchActive
\tif err := decisiongraph.ValidateBranchTransition(current, request.ToState, reopened); err != nil {
\t\treturn err
\t}

\teventHash := eventDigest("branch_transitioned", request.RunID, request.NodeID, current, request.ToState, request.EvidenceHash, request.ReasonCode, request.Actor)
\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    evidence_hash,reason_code,actor,event_hash,created_at
) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11)`,
\t\ts.organizationID, request.RunID, graphVersionID, request.NodeID, current, request.ToState,
\t\trequest.EvidenceHash, request.ReasonCode, request.Actor, eventHash, now); err != nil {
\t\treturn fmt.Errorf("insert branch transition event: %w", err)
\t}
\tcommand, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state=$4, updated_at=$5
WHERE id=$1 AND run_id=$2 AND organization_id=$3 AND branch_state=$6`,
\t\trequest.NodeID, request.RunID, s.organizationID, request.ToState, now, current)
\tif err != nil {
\t\treturn fmt.Errorf("update branch state: %w", err)
\t}
\tif command.RowsAffected() != 1 {
\t\treturn decisiongraph.ErrConflict
\t}
\tif err := tx.Commit(ctx); err != nil {
\t\treturn fmt.Errorf("commit branch transition: %w", err)
\t}
\treturn nil
}

func (s *Store) ClaimReadyNode(ctx context.Context, request decisiongraph.ClaimNodeRequest, now time.Time) (decisiongraph.NodeClaim, error) {
''',
)

# Every terminal decision also records selection of its decision node.
replace(
    "internal/decisiongraph/postgres/store.go",
    '''\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='selected', updated_at=$2
WHERE id=$1`, request.DecisionNodeID, now); err != nil {
\t\treturn fmt.Errorf("complete decision node: %w", err)
\t}
''',
    '''\tdecisionBranchEventHash := eventDigest("branch_transitioned", request.RunID, request.DecisionNodeID, decisiongraph.BranchActive, decisiongraph.BranchSelected, "", "terminal_decision_recorded", request.CreatedBy)
\tif _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    reason_code,actor,event_hash,created_at
) VALUES($1,$2,$3,$4,'active','selected','terminal_decision_recorded',$5,$6,$7)`,
\t\ts.organizationID, request.RunID, graphVersionID, request.DecisionNodeID, request.CreatedBy, decisionBranchEventHash, now); err != nil {
\t\treturn fmt.Errorf("record terminal decision branch transition: %w", err)
\t}
\tif _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='selected', updated_at=$2
WHERE id=$1`, request.DecisionNodeID, now); err != nil {
\t\treturn fmt.Errorf("complete decision node: %w", err)
\t}
''',
)

# Durable branch event table.
replace(
    "migrations/000012_create_durable_decision_graph.up.sql",
    "CREATE TABLE decision_graph_budgets (\n",
    '''CREATE TABLE decision_branch_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id TEXT NOT NULL,
    run_id BIGINT NOT NULL,
    graph_version_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    from_branch_state TEXT NOT NULL CHECK (from_branch_state IN ('active','selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')),
    to_branch_state TEXT NOT NULL CHECK (to_branch_state IN ('active','selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')),
    evidence_hash TEXT CHECK (evidence_hash IS NULL OR evidence_hash ~ '^[0-9a-f]{64}$'),
    reason_code TEXT,
    actor TEXT NOT NULL,
    event_hash TEXT NOT NULL CHECK (event_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT decision_branch_events_node_fk
        FOREIGN KEY (node_id, graph_version_id, run_id, organization_id)
        REFERENCES decision_graph_nodes(id, graph_version_id, run_id, organization_id)
        ON DELETE RESTRICT,
    UNIQUE (run_id, event_hash),
    CHECK (from_branch_state <> to_branch_state),
    CHECK (reason_code IS NULL OR length(trim(reason_code)) BETWEEN 1 AND 120),
    CHECK (length(trim(actor)) BETWEEN 1 AND 200),
    CHECK (to_branch_state <> 'active' OR evidence_hash IS NOT NULL)
);

CREATE INDEX decision_branch_events_node_idx
    ON decision_branch_events (run_id, node_id, created_at, id);

CREATE TABLE decision_graph_budgets (
''',
)

# Reopen guard requires a same-transaction evidence event.
replace(
    "migrations/000012_create_durable_decision_graph.up.sql",
    '''    IF NEW.branch_state <> OLD.branch_state AND NOT (
        OLD.branch_state = 'active'
        AND NEW.branch_state IN ('selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')
    ) THEN
''',
    '''    IF NEW.branch_state <> OLD.branch_state AND NOT (
        (
            OLD.branch_state = 'active'
            AND NEW.branch_state IN ('selected','rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','superseded','inconclusive')
        )
        OR (
            OLD.branch_state IN ('rejected_by_evidence','rejected_by_policy','rejected_by_capability','rejected_by_dependency','rejected_by_budget','inconclusive')
            AND NEW.branch_state = 'active'
            AND EXISTS (
                SELECT 1
                FROM decision_branch_events event
                WHERE event.node_id = OLD.id
                  AND event.run_id = OLD.run_id
                  AND event.organization_id = OLD.organization_id
                  AND event.graph_version_id = OLD.graph_version_id
                  AND event.from_branch_state = OLD.branch_state
                  AND event.to_branch_state = NEW.branch_state
                  AND event.evidence_hash IS NOT NULL
                  AND event.created_at = NEW.updated_at
            )
        )
    ) THEN
''',
)

# Immutable branch events trigger.
replace(
    "migrations/000012_create_durable_decision_graph.up.sql",
    "CREATE TRIGGER decision_graph_versions_immutable\n",
    '''CREATE TRIGGER decision_branch_events_immutable
BEFORE UPDATE OR DELETE ON decision_branch_events
FOR EACH ROW EXECUTE FUNCTION decision_graph_immutable_row();
CREATE TRIGGER decision_graph_versions_immutable
''',
)

# Down migration ordering.
replace(
    "migrations/000012_create_durable_decision_graph.down.sql",
    "DROP TRIGGER IF EXISTS decision_graph_versions_immutable ON decision_graph_versions;\n",
    "DROP TRIGGER IF EXISTS decision_branch_events_immutable ON decision_branch_events;\nDROP TRIGGER IF EXISTS decision_graph_versions_immutable ON decision_graph_versions;\n",
)
replace(
    "migrations/000012_create_durable_decision_graph.down.sql",
    "DROP TABLE IF EXISTS decision_graph_budgets;\n",
    "DROP TABLE IF EXISTS decision_graph_budgets;\nDROP TABLE IF EXISTS decision_branch_events;\n",
)

# Integration uses only the service port and proves evidence-gated reopen.
replace(
    "internal/decisiongraph/postgres/integration_test.go",
    '''\tif _, err := platform.Pool().Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='selected', updated_at=$2
WHERE id=$1`, candidate.NodeID, clock.Now()); err != nil {
\t\tt.Fatalf("select candidate branch: %v", err)
\t}
''',
    '''\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID,
\t\tToState: decisiongraph.BranchRejectedByEvidence,
\t\tReasonCode: "candidate_temporarily_rejected", Actor: "integration/verifier",
\t}); err != nil {
\t\tt.Fatal(err)
\t}
\tif _, err := platform.Pool().Exec(ctx, `
UPDATE decision_graph_nodes SET branch_state='active', updated_at=$2 WHERE id=$1`, candidate.NodeID, clock.Now()); err == nil {
\t\tt.Fatal("expected direct evidence-less branch reopen to fail")
\t}
\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchActive,
\t\tEvidenceHash: digest("new-candidate-evidence"), ReasonCode: "new_evidence", Actor: "integration/verifier",
\t}); err != nil {
\t\tt.Fatal(err)
\t}
\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchSelected,
\t\tReasonCode: "candidate_selected", Actor: "integration/decider",
\t}); err != nil {
\t\tt.Fatal(err)
\t}
''',
)

# Fitness requires the durable branch transition path.
replace(
    "scripts/check-decisiongraph-fitness.sh",
    "rg -q 'decision_graph_edges_cycle_guard' migrations/000012_create_durable_decision_graph.up.sql || fail \"database cycle guard missing\"\n",
    "rg -q 'decision_graph_edges_cycle_guard' migrations/000012_create_durable_decision_graph.up.sql || fail \"database cycle guard missing\"\nrg -q 'TransitionBranch' internal/decisiongraph/ports.go internal/decisiongraph/service.go internal/decisiongraph/postgres/store.go || fail \"durable branch transition port missing\"\nrg -q 'decision_branch_events' migrations/000012_create_durable_decision_graph.up.sql internal/decisiongraph/postgres/store.go || fail \"branch transition ledger missing\"\n",
)
