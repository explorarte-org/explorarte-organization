package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("decisiongraph store requires initialized PostgreSQL")
	}
	if strings.TrimSpace(organizationID) == "" {
		return nil, errors.New("decisiongraph store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

func (s *Store) CreateRun(ctx context.Context, request decisiongraph.CreateRunRequest, now time.Time) (decisiongraph.Run, error) {
	if err := request.Validate(now); err != nil {
		return decisiongraph.Run{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return decisiongraph.Run{}, fmt.Errorf("begin create run: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
INSERT INTO decision_graph_runs (
    organization_id, task_id, attempt_id, status,
    reasoning_policy_schema_version, reasoning_policy_hash, idempotency_key,
    max_nodes, max_depth, max_parallel_nodes, max_model_calls,
    max_input_tokens, max_output_tokens, max_replans, max_verifications,
    max_wall_time_ms, deadline, created_by, created_at, updated_at
) VALUES (
    $1,$2,$3,'planned',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18
)
ON CONFLICT (organization_id, idempotency_key) DO NOTHING
RETURNING id, organization_id, task_id, attempt_id, status,
          reasoning_policy_schema_version, reasoning_policy_hash, idempotency_key,
          deadline, created_by, created_at, updated_at, terminal_at`,
		s.organizationID,
		request.TaskID,
		request.AttemptID,
		request.ReasoningPolicySchemaVersion,
		request.ReasoningPolicyHash,
		request.IdempotencyKey,
		request.BudgetLimits.MaxNodes,
		request.BudgetLimits.MaxDepth,
		request.BudgetLimits.MaxParallelNodes,
		request.BudgetLimits.MaxModelCalls,
		request.BudgetLimits.MaxInputTokens,
		request.BudgetLimits.MaxOutputTokens,
		request.BudgetLimits.MaxReplans,
		request.BudgetLimits.MaxVerifications,
		request.BudgetLimits.MaxWallTime.Milliseconds(),
		request.Deadline,
		request.CreatedBy,
		now,
	)

	run, scanErr := scanRun(row, request.BudgetLimits)
	if scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
		return decisiongraph.Run{}, fmt.Errorf("insert decision graph run: %w", scanErr)
	}
	if errors.Is(scanErr, pgx.ErrNoRows) {
		run, err = s.loadRunByIdempotency(ctx, tx, request.IdempotencyKey)
		if err != nil {
			return decisiongraph.Run{}, err
		}
		if err := sameCreateRequest(run, request); err != nil {
			return decisiongraph.Run{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return decisiongraph.Run{}, fmt.Errorf("commit idempotent create run: %w", err)
		}
		return run, nil
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO decision_graph_budgets (run_id, organization_id, updated_at)
VALUES ($1,$2,$3)`, run.ID, s.organizationID, now); err != nil {
		return decisiongraph.Run{}, fmt.Errorf("initialize decision graph budget: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return decisiongraph.Run{}, fmt.Errorf("commit create run: %w", err)
	}
	return run, nil
}

func (s *Store) AppendGraph(ctx context.Context, request decisiongraph.AppendGraphRequest, now time.Time) (decisiongraph.GraphVersion, error) {
	graph, snapshotHash, maxDepth, err := request.Validate()
	if err != nil {
		return decisiongraph.GraphVersion{}, err
	}
	now = now.UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("begin append graph: %w", err)
	}
	defer tx.Rollback(ctx)

	var status decisiongraph.RunStatus
	var maxNodes, maxDepthLimit, maxReplans int64
	var deadline time.Time
	if err := tx.QueryRow(ctx, `
SELECT status, max_nodes, max_depth, max_replans, deadline
FROM decision_graph_runs
WHERE id=$1 AND organization_id=$2
FOR UPDATE`, request.RunID, s.organizationID).Scan(&status, &maxNodes, &maxDepthLimit, &maxReplans, &deadline); err != nil {
		return decisiongraph.GraphVersion{}, mapNotFound("run", err)
	}
	if status != decisiongraph.RunPlanned && status != decisiongraph.RunRunning {
		return decisiongraph.GraphVersion{}, decisiongraph.ErrRunNotMutable
	}
	if !deadline.After(now) {
		return decisiongraph.GraphVersion{}, decisiongraph.ErrRunDeadlineExceeded
	}

	var usedNodes, usedReplans int64
	if err := tx.QueryRow(ctx, `
SELECT used_nodes, used_replans
FROM decision_graph_budgets
WHERE run_id=$1 AND organization_id=$2
FOR UPDATE`, request.RunID, s.organizationID).Scan(&usedNodes, &usedReplans); err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("lock graph budget: %w", err)
	}

	var previousVersions int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM decision_graph_versions WHERE run_id=$1`, request.RunID).Scan(&previousVersions); err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("count graph versions: %w", err)
	}
	nodeCount := int64(len(graph.Nodes()))
	if nodeCount > maxNodes-usedNodes || int64(maxDepth) > maxDepthLimit {
		return decisiongraph.GraphVersion{}, decisiongraph.ErrBudgetExceeded
	}
	replansDelta := int64(0)
	if previousVersions > 0 {
		replansDelta = 1
		if usedReplans >= maxReplans {
			return decisiongraph.GraphVersion{}, decisiongraph.ErrBudgetExceeded
		}
	}

	versionNumber := previousVersions + 1
	var version decisiongraph.GraphVersion
	if err := tx.QueryRow(ctx, `
INSERT INTO decision_graph_versions (
    organization_id, run_id, version_number, snapshot_hash,
    node_count, max_depth, created_by, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING id, run_id, version_number, snapshot_hash, node_count, max_depth, created_by, created_at`,
		s.organizationID, request.RunID, versionNumber, snapshotHash, nodeCount, maxDepth, request.CreatedBy, now,
	).Scan(&version.ID, &version.RunID, &version.VersionNumber, &version.SnapshotHash, &version.NodeCount, &version.MaxDepth, &version.CreatedBy, &version.CreatedAt); err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("insert graph version: %w", err)
	}

	nodeIDs := make(map[int64]int64, len(request.Nodes))
	for _, node := range graph.Nodes() {
		terminalAt := nullableTerminalTime(node.ExecutionState, now)
		var databaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO decision_graph_nodes (
    organization_id, run_id, graph_version_id, logical_node_id,
    node_type, branch_state, execution_state, payload_schema_version,
    payload_hash, context_snapshot_id, depth, created_by, created_at, updated_at, terminal_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,$14)
RETURNING id`,
			s.organizationID, request.RunID, version.ID, node.ID,
			node.Type, node.BranchState, node.ExecutionState, node.PayloadSchemaVersion,
			node.PayloadHash, node.ContextSnapshotID, request.Depths[node.ID], node.CreatedBy, now, terminalAt,
		).Scan(&databaseID); err != nil {
			return decisiongraph.GraphVersion{}, fmt.Errorf("insert graph node %d: %w", node.ID, err)
		}
		nodeIDs[node.ID] = databaseID
	}
	for _, edge := range graph.Edges() {
		if _, err := tx.Exec(ctx, `
INSERT INTO decision_graph_edges (
    organization_id, run_id, graph_version_id, from_node_id, to_node_id, edge_type, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			s.organizationID, request.RunID, version.ID, nodeIDs[edge.FromNodeID], nodeIDs[edge.ToNodeID], edge.Type, now,
		); err != nil {
			return decisiongraph.GraphVersion{}, fmt.Errorf("insert graph edge: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_budgets
SET used_nodes=used_nodes+$3,
    max_depth_observed=GREATEST(max_depth_observed,$4),
    used_replans=used_replans+$5,
    version=version+1,
    updated_at=$6
WHERE run_id=$1 AND organization_id=$2`, request.RunID, s.organizationID, nodeCount, maxDepth, replansDelta, now); err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("update graph budget: %w", err)
	}
	eventHash := eventDigest("graph_appended", request.RunID, version.ID, snapshotHash)
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, nodes_delta, depth_observed,
    replans_delta, event_hash, created_at
) VALUES ($1,$2,'graph_appended',$3,$4,$5,$6,$7)`,
		s.organizationID, request.RunID, nodeCount, maxDepth, replansDelta, eventHash, now); err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("insert graph budget event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return decisiongraph.GraphVersion{}, fmt.Errorf("commit append graph: %w", err)
	}
	version.NodeIDs = nodeIDs
	return version, nil
}

func (s *Store) StartRun(ctx context.Context, runID int64, now time.Time) error {
	command, err := s.pool.Exec(ctx, `
UPDATE decision_graph_runs r
SET status='running', updated_at=$3
WHERE r.id=$1 AND r.organization_id=$2
  AND r.status='planned'
  AND r.deadline>$3
  AND EXISTS (SELECT 1 FROM decision_graph_versions v WHERE v.run_id=r.id)`, runID, s.organizationID, now)
	if err != nil {
		return fmt.Errorf("start run: %w", err)
	}
	if command.RowsAffected() == 1 {
		return nil
	}
	var status decisiongraph.RunStatus
	var deadline time.Time
	if err := s.pool.QueryRow(ctx, `SELECT status, deadline FROM decision_graph_runs WHERE id=$1 AND organization_id=$2`, runID, s.organizationID).Scan(&status, &deadline); err != nil {
		return mapNotFound("run", err)
	}
	if status == decisiongraph.RunRunning {
		return nil
	}
	if !deadline.After(now) {
		return decisiongraph.ErrRunDeadlineExceeded
	}
	return decisiongraph.ErrRunNotMutable
}

func (s *Store) TransitionBranch(ctx context.Context, request decisiongraph.BranchTransitionRequest, now time.Time) error {
	if err := request.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin branch transition: %w", err)
	}
	defer tx.Rollback(ctx)

	var runStatus decisiongraph.RunStatus
	var graphVersionID int64
	var current decisiongraph.BranchState
	if err := tx.QueryRow(ctx, `
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
		return mapNotFound("branch node", err)
	}
	if runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
		return decisiongraph.ErrRunNotActive
	}
	reopened := request.ToState == decisiongraph.BranchActive
	if err := decisiongraph.ValidateBranchTransition(current, request.ToState, reopened); err != nil {
		return err
	}

	eventHash := eventDigest("branch_transitioned", request.RunID, request.NodeID, current, request.ToState, request.EvidenceHash, request.ReasonCode, request.Actor, now.UTC().Format(time.RFC3339Nano))
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    evidence_hash,reason_code,actor,event_hash,created_at
) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11)`,
		s.organizationID, request.RunID, graphVersionID, request.NodeID, current, request.ToState,
		request.EvidenceHash, request.ReasonCode, request.Actor, eventHash, now); err != nil {
		return fmt.Errorf("insert branch transition event: %w", err)
	}
	command, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state=$4, updated_at=$5
WHERE id=$1 AND run_id=$2 AND organization_id=$3 AND branch_state=$6`,
		request.NodeID, request.RunID, s.organizationID, request.ToState, now, current)
	if err != nil {
		return fmt.Errorf("update branch state: %w", err)
	}
	if command.RowsAffected() != 1 {
		return decisiongraph.ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit branch transition: %w", err)
	}
	return nil
}

func (s *Store) ClaimReadyNode(ctx context.Context, request decisiongraph.ClaimNodeRequest, now time.Time) (decisiongraph.NodeClaim, error) {
	if err := request.Validate(); err != nil {
		return decisiongraph.NodeClaim{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("begin claim node: %w", err)
	}
	defer tx.Rollback(ctx)

	var status decisiongraph.RunStatus
	var deadline time.Time
	var maxParallel, maxCalls, maxVerifications int64
	if err := tx.QueryRow(ctx, `
SELECT status, deadline, max_parallel_nodes, max_model_calls, max_verifications
FROM decision_graph_runs
WHERE id=$1 AND organization_id=$2
FOR UPDATE`, request.RunID, s.organizationID).Scan(&status, &deadline, &maxParallel, &maxCalls, &maxVerifications); err != nil {
		return decisiongraph.NodeClaim{}, mapNotFound("run", err)
	}
	if status != decisiongraph.RunRunning {
		return decisiongraph.NodeClaim{}, decisiongraph.ErrRunNotActive
	}
	if !deadline.After(now) {
		return decisiongraph.NodeClaim{}, decisiongraph.ErrRunDeadlineExceeded
	}

	var activeParallel, usedCalls, usedVerifications int64
	if err := tx.QueryRow(ctx, `
SELECT active_parallel_nodes, used_model_calls, used_verifications
FROM decision_graph_budgets
WHERE run_id=$1 AND organization_id=$2
FOR UPDATE`, request.RunID, s.organizationID).Scan(&activeParallel, &usedCalls, &usedVerifications); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("lock claim budget: %w", err)
	}
	if activeParallel >= maxParallel || usedCalls >= maxCalls {
		return decisiongraph.NodeClaim{}, decisiongraph.ErrBudgetExceeded
	}

	var nodeID, graphVersionID, logicalNodeID int64
	var nodeType decisiongraph.NodeType
	if err := tx.QueryRow(ctx, `
WITH latest AS (
    SELECT id
    FROM decision_graph_versions
    WHERE run_id=$1 AND organization_id=$2
    ORDER BY version_number DESC
    LIMIT 1
)
SELECT n.id, n.graph_version_id, n.logical_node_id, n.node_type
FROM decision_graph_nodes n
JOIN latest l ON l.id=n.graph_version_id
WHERE n.run_id=$1
  AND n.organization_id=$2
  AND n.branch_state='active'
  AND n.execution_state IN ('pending','ready')
  AND NOT EXISTS (
      SELECT 1
      FROM decision_graph_edges e
      JOIN decision_graph_nodes dependency ON dependency.id=e.to_node_id
      WHERE e.graph_version_id=n.graph_version_id
        AND e.from_node_id=n.id
        AND e.edge_type='depends_on'
        AND dependency.execution_state<>'succeeded'
  )
  AND NOT EXISTS (
      SELECT 1 FROM decision_node_executions x
      WHERE x.node_id=n.id
        AND x.status IN ('claimed','running','waiting_verification')
  )
ORDER BY n.depth, n.id
FOR UPDATE OF n SKIP LOCKED
LIMIT 1`, request.RunID, s.organizationID).Scan(&nodeID, &graphVersionID, &logicalNodeID, &nodeType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return decisiongraph.NodeClaim{}, decisiongraph.ErrClaimUnavailable
		}
		return decisiongraph.NodeClaim{}, fmt.Errorf("select ready node: %w", err)
	}
	_ = usedVerifications
	_ = maxVerifications

	token, tokenHash, err := newClaimToken()
	if err != nil {
		return decisiongraph.NodeClaim{}, err
	}
	var attemptNumber int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(attempt_number),0)+1 FROM decision_node_executions WHERE node_id=$1`, nodeID).Scan(&attemptNumber); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("allocate node attempt: %w", err)
	}
	expiresAt := now.Add(request.LeaseDuration)
	var executionID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO decision_node_executions (
    organization_id, run_id, graph_version_id, node_id, attempt_number,
    status, claim_token_hash, claimed_by, claimed_at, claim_expires_at,
    started_at, created_at
) VALUES ($1,$2,$3,$4,$5,'running',$6,$7,$8,$9,$8,$8)
RETURNING id`, s.organizationID, request.RunID, graphVersionID, nodeID, attemptNumber, tokenHash, request.ClaimedBy, now, expiresAt).Scan(&executionID); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("insert node execution: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET execution_state='running', updated_at=$2
WHERE id=$1`, nodeID, now); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("mark node running: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_budgets
SET active_parallel_nodes=active_parallel_nodes+1,
    used_model_calls=used_model_calls+1,
    version=version+1,
    updated_at=$3
WHERE run_id=$1 AND organization_id=$2`, request.RunID, s.organizationID, now); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("reserve claim budget: %w", err)
	}
	eventHash := eventDigest("node_claimed", request.RunID, executionID, tokenHash)
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta,
    model_calls_delta, event_hash, created_at
) VALUES ($1,$2,'node_claimed',1,1,$3,$4)`, s.organizationID, request.RunID, eventHash, now); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("insert claim budget event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return decisiongraph.NodeClaim{}, fmt.Errorf("commit claim node: %w", err)
	}
	return decisiongraph.NodeClaim{
		ExecutionID:    executionID,
		RunID:          request.RunID,
		GraphVersionID: graphVersionID,
		NodeID:         nodeID,
		LogicalNodeID:  logicalNodeID,
		NodeType:       nodeType,
		ClaimToken:     token,
		ClaimExpiresAt: expiresAt,
	}, nil
}

func (s *Store) FinishExecution(ctx context.Context, request decisiongraph.FinishExecutionRequest, now time.Time) error {
	if err := request.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin finish execution: %w", err)
	}
	defer tx.Rollback(ctx)

	var runID, nodeID int64
	var status string
	var tokenHash string
	var claimedAt, expiresAt time.Time
	if err := tx.QueryRow(ctx, `
SELECT run_id, node_id, status, claim_token_hash, claimed_at, claim_expires_at
FROM decision_node_executions
WHERE id=$1 AND organization_id=$2
FOR UPDATE`, request.ExecutionID, s.organizationID).Scan(&runID, &nodeID, &status, &tokenHash, &claimedAt, &expiresAt); err != nil {
		return mapNotFound("execution", err)
	}
	if status != "running" {
		return decisiongraph.ErrStaleClaim
	}
	if claimDigest(request.ClaimToken) != tokenHash || !expiresAt.After(now) {
		return decisiongraph.ErrStaleClaim
	}

	if request.ModelInvocationID != nil {
		var taskID, attemptID int64
		if err := tx.QueryRow(ctx, `
SELECT r.task_id, r.attempt_id
FROM decision_graph_runs r
JOIN model_invocations mi
  ON mi.organization_id=r.organization_id
 AND mi.task_id=r.task_id
 AND mi.attempt_id=r.attempt_id
WHERE r.id=$1 AND r.organization_id=$2
  AND mi.id=$3`, runID, s.organizationID, *request.ModelInvocationID).Scan(&taskID, &attemptID); err != nil {
			return fmt.Errorf("validate runtime linkage: %w", err)
		}
	}

	executionStatus := string(request.FinalState)
	finishedAt := any(now)
	if request.FinalState == decisiongraph.ExecutionWaitingVerification {
		executionStatus = "waiting_verification"
		finishedAt = nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE decision_node_executions
SET status=$3,
    finished_at=$4,
    model_invocation_id=$5,
    dispatch_attempt_id=$6,
    outcome_hash=NULLIF($7,''),
    reason_code=NULLIF($8,''),
    input_tokens=$9,
    output_tokens=$10
WHERE id=$1 AND organization_id=$2`, request.ExecutionID, s.organizationID, executionStatus, finishedAt,
		request.ModelInvocationID, request.DispatchAttemptID, request.OutcomeHash, request.ReasonCode,
		request.InputTokens, request.OutputTokens); err != nil {
		return fmt.Errorf("update node execution: %w", err)
	}
	nodeTerminalAt := any(now)
	if request.FinalState == decisiongraph.ExecutionWaitingVerification {
		nodeTerminalAt = nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET execution_state=$2, updated_at=$3, terminal_at=$4
WHERE id=$1`, nodeID, request.FinalState, now, nodeTerminalAt); err != nil {
		return fmt.Errorf("update node state: %w", err)
	}

	var maxInput, maxOutput, maxWallTimeMS int64
	if err := tx.QueryRow(ctx, `SELECT max_input_tokens, max_output_tokens, max_wall_time_ms FROM decision_graph_runs WHERE id=$1 AND organization_id=$2 FOR UPDATE`, runID, s.organizationID).Scan(&maxInput, &maxOutput, &maxWallTimeMS); err != nil {
		return fmt.Errorf("lock run execution budget: %w", err)
	}
	var usedInput, usedOutput, usedWallTimeMS, active int64
	if err := tx.QueryRow(ctx, `
SELECT used_input_tokens, used_output_tokens, used_wall_time_ms, active_parallel_nodes
FROM decision_graph_budgets
WHERE run_id=$1 AND organization_id=$2
FOR UPDATE`, runID, s.organizationID).Scan(&usedInput, &usedOutput, &usedWallTimeMS, &active); err != nil {
		return fmt.Errorf("lock finish budget: %w", err)
	}
	if active <= 0 {
		return decisiongraph.ErrInvalidBudget
	}
	elapsedMS := elapsedMilliseconds(claimedAt, now)
	overBudget := usedInput > maxInput || request.InputTokens > maxInput-usedInput ||
		usedOutput > maxOutput || request.OutputTokens > maxOutput-usedOutput ||
		usedWallTimeMS > maxWallTimeMS || elapsedMS > maxWallTimeMS-usedWallTimeMS
	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_budgets
SET active_parallel_nodes=active_parallel_nodes-1,
    used_input_tokens=used_input_tokens+$3,
    used_output_tokens=used_output_tokens+$4,
    used_wall_time_ms=used_wall_time_ms+$5,
    version=version+1,
    updated_at=$6
WHERE run_id=$1 AND organization_id=$2`, runID, s.organizationID, request.InputTokens, request.OutputTokens, elapsedMS, now); err != nil {
		return fmt.Errorf("update finish budget: %w", err)
	}
	if overBudget {
		if _, err := tx.Exec(ctx, `
UPDATE decision_graph_runs
SET status='failed', terminal_at=$3, terminal_reason_code='budget_exceeded', updated_at=$3
WHERE id=$1 AND organization_id=$2
  AND status IN ('planned','running','waiting')`, runID, s.organizationID, now); err != nil {
			return fmt.Errorf("fail over-budget run: %w", err)
		}
		branchEventHash := eventDigest("branch_transitioned", runID, nodeID, decisiongraph.BranchActive, decisiongraph.BranchRejectedByBudget, "", "budget_exceeded", "decisiongraph/budget")
		if _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    reason_code,actor,event_hash,created_at
)
SELECT organization_id,run_id,graph_version_id,id,'active','rejected_by_budget',
       'budget_exceeded','decisiongraph/budget',$3,$4
FROM decision_graph_nodes
WHERE id=$1 AND run_id=$2 AND branch_state='active'`, nodeID, runID, branchEventHash, now); err != nil {
			return fmt.Errorf("record over-budget branch event: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='rejected_by_budget', updated_at=$2
WHERE id=$1 AND branch_state='active'`, nodeID, now); err != nil {
			return fmt.Errorf("reject over-budget branch: %w", err)
		}
	}
	eventHash := eventDigest("execution_finished", runID, request.ExecutionID, request.OutcomeHash, fmt.Sprint(request.InputTokens), fmt.Sprint(request.OutputTokens))
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta,
    input_tokens_delta, output_tokens_delta, wall_time_ms_delta, event_hash, created_at
) VALUES ($1,$2,'execution_finished',-1,$3,$4,$5,$6,$7)`, s.organizationID, runID, request.InputTokens, request.OutputTokens, elapsedMS, eventHash, now); err != nil {
		return fmt.Errorf("insert finish budget event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finish execution: %w", err)
	}
	if overBudget {
		return decisiongraph.ErrBudgetExceeded
	}
	return nil
}

func (s *Store) RecordObservation(ctx context.Context, record decisiongraph.ObservationRecord, now time.Time) error {
	if err := record.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin record observation: %w", err)
	}
	defer tx.Rollback(ctx)

	var runID, nodeID int64
	var runStatus decisiongraph.RunStatus
	if err := tx.QueryRow(ctx, `
SELECT x.run_id, x.node_id, r.status
FROM decision_node_executions x
JOIN decision_graph_runs r
  ON r.id=x.run_id AND r.organization_id=x.organization_id
WHERE x.id=$1 AND x.organization_id=$2
FOR UPDATE OF x,r`, record.ExecutionID, s.organizationID).Scan(&runID, &nodeID, &runStatus); err != nil {
		return mapNotFound("observation execution", err)
	}
	if runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
		return decisiongraph.ErrRunNotActive
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_observations (
    organization_id, run_id, node_id, execution_id, schema_version,
    observation_hash, source_kind, source_reference_hash, created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)`,
		s.organizationID, runID, nodeID, record.ExecutionID, record.SchemaVersion,
		record.ObservationHash, record.SourceKind, record.SourceReferenceHash, now); err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit observation: %w", err)
	}
	return nil
}

func (s *Store) RecordVerification(ctx context.Context, record decisiongraph.VerificationRecord, now time.Time) error {
	if err := record.Validate(); err != nil {
		return err
	}
	reasonCodes, err := json.Marshal(record.ReasonCodes)
	if err != nil {
		return fmt.Errorf("marshal verification reason codes: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin record verification: %w", err)
	}
	defer tx.Rollback(ctx)

	var runStatus decisiongraph.RunStatus
	var maxVerifications, usedVerifications int64
	if err := tx.QueryRow(ctx, `
SELECT r.status, r.max_verifications, b.used_verifications
FROM decision_graph_runs r
JOIN decision_graph_budgets b
  ON b.run_id=r.id AND b.organization_id=r.organization_id
WHERE r.id=$1 AND r.organization_id=$2
FOR UPDATE OF r,b`, record.RunID, s.organizationID).Scan(&runStatus, &maxVerifications, &usedVerifications); err != nil {
		return mapNotFound("run", err)
	}
	if runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
		return decisiongraph.ErrRunNotActive
	}
	if usedVerifications >= maxVerifications {
		return decisiongraph.ErrBudgetExceeded
	}

	var nodeExecutionState decisiongraph.ExecutionState
	if err := tx.QueryRow(ctx, `
SELECT execution_state
FROM decision_graph_nodes
WHERE id=$1 AND run_id=$2 AND organization_id=$3
FOR UPDATE`, record.NodeID, record.RunID, s.organizationID).Scan(&nodeExecutionState); err != nil {
		return mapNotFound("verification node target", err)
	}
	if record.ExecutionID != nil {
		var executionStatus string
		if err := tx.QueryRow(ctx, `
SELECT status
FROM decision_node_executions
WHERE id=$1 AND node_id=$2 AND run_id=$3 AND organization_id=$4
FOR UPDATE`, *record.ExecutionID, record.NodeID, record.RunID, s.organizationID).Scan(&executionStatus); err != nil {
			return mapNotFound("verification execution", err)
		}
		if executionStatus != "waiting_verification" && executionStatus != "running" {
			return decisiongraph.ErrInvalidVerification
		}
	}

	var verificationID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO decision_verifications (
    organization_id, run_id, node_id, execution_id, label,
    verifier_ref, verifier_version, evidence_set_hash, reason_codes, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10)
RETURNING id`, s.organizationID, record.RunID, record.NodeID, record.ExecutionID,
		record.Label, record.VerifierRef, record.VerifierVersion, record.EvidenceSetHash,
		string(reasonCodes), now).Scan(&verificationID); err != nil {
		return fmt.Errorf("insert verification: %w", err)
	}

	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_budgets
SET used_verifications=used_verifications+1,
    version=version+1,
    updated_at=$3
WHERE run_id=$1 AND organization_id=$2`, record.RunID, s.organizationID, now); err != nil {
		return fmt.Errorf("consume verification budget: %w", err)
	}
	eventHash := eventDigest("verification_recorded", record.RunID, verificationID, record.EvidenceSetHash, record.Label)
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, verifications_delta, event_hash, created_at
) VALUES ($1,$2,'verification_recorded',1,$3,$4)`, s.organizationID, record.RunID, eventHash, now); err != nil {
		return fmt.Errorf("insert verification budget event: %w", err)
	}

	if record.ExecutionID != nil && nodeExecutionState == decisiongraph.ExecutionWaitingVerification {
		var finalState decisiongraph.ExecutionState
		switch record.Label {
		case decisiongraph.VerificationVerified, decisiongraph.VerificationInferred:
			finalState = decisiongraph.ExecutionSucceeded
		case decisiongraph.VerificationContradicted:
			finalState = decisiongraph.ExecutionFailed
		case decisiongraph.VerificationUnknown:
			finalState = decisiongraph.ExecutionWaitingVerification
		default:
			return decisiongraph.ErrInvalidVerification
		}
		if finalState != decisiongraph.ExecutionWaitingVerification {
			if _, err := tx.Exec(ctx, `
UPDATE decision_node_executions
SET status=$2, finished_at=$3,
    reason_code=CASE WHEN $2='failed' THEN 'verification_contradicted' ELSE reason_code END
WHERE id=$1`, *record.ExecutionID, finalState, now); err != nil {
				return fmt.Errorf("finish verified execution: %w", err)
			}
			if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET execution_state=$2, terminal_at=$3, updated_at=$3
WHERE id=$1`, record.NodeID, finalState, now); err != nil {
				return fmt.Errorf("finish verified node: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verification: %w", err)
	}
	return nil
}

func (s *Store) RecordTerminalDecision(ctx context.Context, request decisiongraph.TerminalDecisionRequest, now time.Time) error {
	if err := request.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin terminal decision: %w", err)
	}
	defer tx.Rollback(ctx)

	var runStatus decisiongraph.RunStatus
	var latestGraphVersionID int64
	if err := tx.QueryRow(ctx, `
SELECT r.status, v.id
FROM decision_graph_runs r
JOIN LATERAL (
    SELECT id FROM decision_graph_versions
    WHERE run_id=r.id AND organization_id=r.organization_id
    ORDER BY version_number DESC LIMIT 1
) v ON TRUE
WHERE r.id=$1 AND r.organization_id=$2
FOR UPDATE OF r`, request.RunID, s.organizationID).Scan(&runStatus, &latestGraphVersionID); err != nil {
		return mapNotFound("run", err)
	}
	if runStatus != decisiongraph.RunRunning && runStatus != decisiongraph.RunWaiting {
		return decisiongraph.ErrRunNotActive
	}

	var graphVersionID int64
	var decisionType, candidateType decisiongraph.NodeType
	var decisionExecution, candidateExecution decisiongraph.ExecutionState
	var candidateBranch decisiongraph.BranchState
	if err := tx.QueryRow(ctx, `
SELECT d.graph_version_id, d.node_type, d.execution_state, c.node_type, c.branch_state, c.execution_state
FROM decision_graph_nodes d
JOIN decision_graph_nodes c
  ON c.id=$4
 AND c.graph_version_id=d.graph_version_id
 AND c.run_id=d.run_id
 AND c.organization_id=d.organization_id
WHERE d.id=$3 AND d.run_id=$1 AND d.organization_id=$2
FOR UPDATE OF d,c`, request.RunID, s.organizationID, request.DecisionNodeID, request.SelectedCandidateNodeID).Scan(
		&graphVersionID, &decisionType, &decisionExecution, &candidateType, &candidateBranch, &candidateExecution,
	); err != nil {
		return mapNotFound("decision nodes", err)
	}
	if decisionType != decisiongraph.NodeDecision || decisionExecution != decisiongraph.ExecutionSucceeded || candidateType != decisiongraph.NodeCandidateAction || candidateBranch != decisiongraph.BranchSelected || candidateExecution != decisiongraph.ExecutionSucceeded {
		return decisiongraph.ErrInvalidDecision
	}
	if graphVersionID != latestGraphVersionID {
		return decisiongraph.ErrInvalidDecision
	}
	var supportingVerifications int
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM decision_verifications
WHERE run_id=$1 AND organization_id=$2
  AND node_id=$3
  AND label IN ('verified','inferred')`, request.RunID, s.organizationID, request.SelectedCandidateNodeID).Scan(&supportingVerifications); err != nil {
		return fmt.Errorf("count supporting verifications: %w", err)
	}
	if supportingVerifications == 0 {
		return decisiongraph.ErrInvalidDecision
	}
	var activeExecutions int
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM decision_node_executions
WHERE run_id=$1 AND organization_id=$2
  AND status IN ('claimed','running','waiting_verification')`, request.RunID, s.organizationID).Scan(&activeExecutions); err != nil {
		return fmt.Errorf("count active decision executions: %w", err)
	}
	if activeExecutions != 0 {
		return decisiongraph.ErrRunNotMutable
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_records (
    organization_id, run_id, graph_version_id, decision_node_id,
    selected_candidate_node_id, evidence_set_hash, verification_set_hash,
    decision_hash, verification_label, created_by, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		s.organizationID, request.RunID, graphVersionID, request.DecisionNodeID,
		request.SelectedCandidateNodeID, request.EvidenceSetHash, request.VerificationSetHash,
		request.DecisionHash, request.VerificationLabel, request.CreatedBy, now); err != nil {
		return fmt.Errorf("insert terminal decision: %w", err)
	}
	decisionBranchEventHash := eventDigest("branch_transitioned", request.RunID, request.DecisionNodeID, decisiongraph.BranchActive, decisiongraph.BranchSelected, "", "terminal_decision_recorded", request.CreatedBy)
	if _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    reason_code,actor,event_hash,created_at
) VALUES($1,$2,$3,$4,'active','selected','terminal_decision_recorded',$5,$6,$7)`,
		s.organizationID, request.RunID, graphVersionID, request.DecisionNodeID, request.CreatedBy, decisionBranchEventHash, now); err != nil {
		return fmt.Errorf("record terminal decision branch transition: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='selected', updated_at=$2
WHERE id=$1`, request.DecisionNodeID, now); err != nil {
		return fmt.Errorf("complete decision node: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE decision_graph_runs
SET status='succeeded', terminal_at=$3, terminal_reason_code='terminal_decision_recorded', updated_at=$3
WHERE id=$1 AND organization_id=$2 AND status IN ('running','waiting')`, request.RunID, s.organizationID, now); err != nil {
		return fmt.Errorf("complete decision graph run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit terminal decision: %w", err)
	}
	return nil
}

func (s *Store) RecoverExpiredExecutions(ctx context.Context, limit int, now time.Time) (int, error) {
	if limit < 1 || limit > 256 {
		return 0, decisiongraph.ErrInvalidExecution
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, fmt.Errorf("begin execution recovery: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT x.id, x.run_id, x.node_id, x.status, x.claimed_at, x.model_invocation_id, mi.status
FROM decision_node_executions x
LEFT JOIN model_invocations mi
  ON mi.id=x.model_invocation_id
 AND mi.organization_id=x.organization_id
WHERE x.organization_id=$1
  AND x.status IN ('claimed','running','waiting_verification')
  AND x.claim_expires_at<=$2
ORDER BY x.claim_expires_at, x.id
FOR UPDATE OF x SKIP LOCKED
LIMIT $3`, s.organizationID, now, limit)
	if err != nil {
		return 0, fmt.Errorf("select expired executions: %w", err)
	}
	defer rows.Close()

	type expiredExecution struct {
		id               int64
		runID            int64
		nodeID           int64
		status           string
		claimedAt        time.Time
		invocationID     *int64
		invocationStatus *string
	}
	expired := make([]expiredExecution, 0, limit)
	for rows.Next() {
		var item expiredExecution
		if err := rows.Scan(&item.id, &item.runID, &item.nodeID, &item.status, &item.claimedAt, &item.invocationID, &item.invocationStatus); err != nil {
			return 0, fmt.Errorf("scan expired execution: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate expired executions: %w", err)
	}

	for _, item := range expired {
		finalState := decisiongraph.ExecutionFailed
		reasonCode := "claim_expired_before_runtime_link"
		if item.invocationStatus != nil {
			switch *item.invocationStatus {
			case "succeeded":
				finalState = decisiongraph.ExecutionSucceeded
				reasonCode = "runtime_succeeded_before_claim_expiry"
			case "failed":
				finalState = decisiongraph.ExecutionFailed
				reasonCode = "runtime_failed_before_claim_expiry"
			case "cancelled":
				finalState = decisiongraph.ExecutionCancelled
				reasonCode = "runtime_cancelled_before_claim_expiry"
			case "ambiguous", "claimed", "send_started", "response_received":
				finalState = decisiongraph.ExecutionAmbiguous
				reasonCode = "runtime_outcome_ambiguous_after_claim_expiry"
			default:
				finalState = decisiongraph.ExecutionFailed
				reasonCode = "claim_expired_before_send"
			}
		} else if item.status == "waiting_verification" {
			finalState = decisiongraph.ExecutionAmbiguous
			reasonCode = "verification_not_recorded_before_claim_expiry"
		}

		if _, err := tx.Exec(ctx, `
UPDATE decision_node_executions
SET status=$2, finished_at=$3, reason_code=$4
WHERE id=$1`, item.id, finalState, now, reasonCode); err != nil {
			return 0, fmt.Errorf("recover node execution %d: %w", item.id, err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET execution_state=$2, terminal_at=$3, updated_at=$3
WHERE id=$1`, item.nodeID, finalState, now); err != nil {
			return 0, fmt.Errorf("recover decision node %d: %w", item.nodeID, err)
		}
		parallelDelta := 0
		wallTimeDeltaMS := int64(0)
		overWallBudget := false
		if item.status == "claimed" || item.status == "running" {
			parallelDelta = -1
			wallTimeDeltaMS = elapsedMilliseconds(item.claimedAt, now)
			var usedWallTimeMS, maxWallTimeMS int64
			if err := tx.QueryRow(ctx, `
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
				return 0, fmt.Errorf("release recovered execution budget: %w", err)
			}
			overWallBudget = usedWallTimeMS > maxWallTimeMS
		}
		if overWallBudget && finalState != decisiongraph.ExecutionAmbiguous {
			if _, err := tx.Exec(ctx, `
UPDATE decision_graph_runs
SET status='failed', terminal_at=$3,
    terminal_reason_code='budget_exceeded', updated_at=$3
WHERE id=$1 AND organization_id=$2
  AND status IN ('planned','running','waiting')`, item.runID, s.organizationID, now); err != nil {
				return 0, fmt.Errorf("fail recovered over-budget run: %w", err)
			}
			branchEventHash := eventDigest("branch_transitioned", item.runID, item.nodeID, decisiongraph.BranchActive, decisiongraph.BranchRejectedByBudget, "", "budget_exceeded", "decisiongraph/recovery", now.UTC().Format(time.RFC3339Nano))
			if _, err := tx.Exec(ctx, `
INSERT INTO decision_branch_events(
    organization_id,run_id,graph_version_id,node_id,from_branch_state,to_branch_state,
    reason_code,actor,event_hash,created_at
)
SELECT organization_id,run_id,graph_version_id,id,'active','rejected_by_budget',
       'budget_exceeded','decisiongraph/recovery',$3,$4
FROM decision_graph_nodes
WHERE id=$1 AND run_id=$2 AND branch_state='active'`, item.nodeID, item.runID, branchEventHash, now); err != nil {
				return 0, fmt.Errorf("record recovered over-budget branch event: %w", err)
			}
			if _, err := tx.Exec(ctx, `
UPDATE decision_graph_nodes
SET branch_state='rejected_by_budget', updated_at=$2
WHERE id=$1 AND branch_state='active'`, item.nodeID, now); err != nil {
				return 0, fmt.Errorf("reject recovered over-budget branch: %w", err)
			}
		}
		if finalState == decisiongraph.ExecutionAmbiguous {
			if _, err := tx.Exec(ctx, `
UPDATE decision_graph_runs
SET status='ambiguous', terminal_at=$3,
    terminal_reason_code=$4, updated_at=$3
WHERE id=$1 AND organization_id=$2
  AND status IN ('planned','running','waiting')`, item.runID, s.organizationID, now, reasonCode); err != nil {
				return 0, fmt.Errorf("mark recovered run ambiguous: %w", err)
			}
		}
		eventHash := eventDigest("execution_recovered", item.runID, item.id, finalState, reasonCode)
		if _, err := tx.Exec(ctx, `
INSERT INTO decision_budget_events (
    organization_id, run_id, event_kind, parallel_delta, wall_time_ms_delta, event_hash, created_at
) VALUES ($1,$2,'execution_finished',$3,$4,$5,$6)`, s.organizationID, item.runID, parallelDelta, wallTimeDeltaMS, eventHash, now); err != nil {
			return 0, fmt.Errorf("record recovered execution event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit execution recovery: %w", err)
	}
	return len(expired), nil
}

func (s *Store) TraceRef(ctx context.Context, runID int64) (decisiongraph.TraceRef, error) {
	var policyHash, graphHash, decisionHash string
	var executionTrace, observationTrace, verificationTrace, branchTrace, budgetTrace string
	if err := s.pool.QueryRow(ctx, `
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
		&policyHash, &graphHash, &decisionHash,
		&executionTrace, &observationTrace, &verificationTrace, &branchTrace, &budgetTrace,
	); err != nil {
		return decisiongraph.TraceRef{}, mapNotFound("trace", err)
	}
	traceHash := eventDigest(
		"decision-trace/v1", runID, policyHash, graphHash, decisionHash,
		executionTrace, observationTrace, verificationTrace, branchTrace, budgetTrace,
	)
	return decisiongraph.TraceRef{
		OrganizationID: s.organizationID,
		RunID:          runID,
		TraceHash:      traceHash,
		SchemaVersion:  "decision-trace/v1",
	}, nil
}

func scanRun(row pgx.Row, limits decisiongraph.BudgetLimits) (decisiongraph.Run, error) {
	var run decisiongraph.Run
	err := row.Scan(
		&run.ID,
		&run.OrganizationID,
		&run.TaskID,
		&run.AttemptID,
		&run.Status,
		&run.ReasoningPolicySchemaVersion,
		&run.ReasoningPolicyHash,
		&run.IdempotencyKey,
		&run.Deadline,
		&run.CreatedBy,
		&run.CreatedAt,
		&run.UpdatedAt,
		&run.TerminalAt,
	)
	run.BudgetLimits = limits
	return run, err
}

func (s *Store) loadRunByIdempotency(ctx context.Context, tx pgx.Tx, key string) (decisiongraph.Run, error) {
	var limits decisiongraph.BudgetLimits
	var wallTimeMS int64
	row := tx.QueryRow(ctx, `
SELECT id, organization_id, task_id, attempt_id, status,
       reasoning_policy_schema_version, reasoning_policy_hash, idempotency_key,
       deadline, created_by, created_at, updated_at, terminal_at,
       max_nodes, max_depth, max_parallel_nodes, max_model_calls,
       max_input_tokens, max_output_tokens, max_replans, max_verifications, max_wall_time_ms
FROM decision_graph_runs
WHERE organization_id=$1 AND idempotency_key=$2
FOR UPDATE`, s.organizationID, key)
	var run decisiongraph.Run
	if err := row.Scan(
		&run.ID, &run.OrganizationID, &run.TaskID, &run.AttemptID, &run.Status,
		&run.ReasoningPolicySchemaVersion, &run.ReasoningPolicyHash, &run.IdempotencyKey,
		&run.Deadline, &run.CreatedBy, &run.CreatedAt, &run.UpdatedAt, &run.TerminalAt,
		&limits.MaxNodes, &limits.MaxDepth, &limits.MaxParallelNodes, &limits.MaxModelCalls,
		&limits.MaxInputTokens, &limits.MaxOutputTokens, &limits.MaxReplans, &limits.MaxVerifications, &wallTimeMS,
	); err != nil {
		return decisiongraph.Run{}, mapNotFound("run", err)
	}
	limits.MaxWallTime = time.Duration(wallTimeMS) * time.Millisecond
	run.BudgetLimits = limits
	return run, nil
}

func sameCreateRequest(run decisiongraph.Run, request decisiongraph.CreateRunRequest) error {
	if run.TaskID != request.TaskID || run.AttemptID != request.AttemptID ||
		run.ReasoningPolicySchemaVersion != request.ReasoningPolicySchemaVersion ||
		run.ReasoningPolicyHash != request.ReasoningPolicyHash ||
		run.IdempotencyKey != request.IdempotencyKey ||
		!run.Deadline.Equal(request.Deadline) ||
		run.CreatedBy != request.CreatedBy || run.BudgetLimits != request.BudgetLimits {
		return decisiongraph.ErrIdempotencyConflict
	}
	return nil
}

func nullableTerminalTime(state decisiongraph.ExecutionState, now time.Time) any {
	switch state {
	case decisiongraph.ExecutionSucceeded, decisiongraph.ExecutionFailed, decisiongraph.ExecutionCancelled, decisiongraph.ExecutionAmbiguous:
		return now
	default:
		return nil
	}
}

func newClaimToken() (string, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("generate claim token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	return token, claimDigest(token), nil
}

func claimDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func elapsedMilliseconds(start, end time.Time) int64 {
	if !end.After(start) {
		return 0
	}
	duration := end.Sub(start)
	milliseconds := duration.Milliseconds()
	if duration%time.Millisecond != 0 {
		milliseconds++
	}
	return milliseconds
}

func eventDigest(kind string, runID int64, values ...any) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%d", kind, runID)
	for _, value := range values {
		fmt.Fprintf(hash, "\n%v", value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func mapNotFound(subject string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", decisiongraph.ErrNotFound, subject)
	}
	return err
}
