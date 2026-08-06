package decisiongraphtrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the PostgreSQL-backed TraceSource for a decisiongraph run,
// scoped to a single organization. It owns no durable state of its own:
// everything it reads belongs to Rama 14's migration 000012.
type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("decisiongraphtrace store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("decisiongraphtrace store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var _ evaluation.TraceSource = (*Store)(nil)

// TraceRefForRun builds the opaque TraceRef for a terminal decisiongraph
// run: run ID, organization, this package's schema version, and a hash of
// its own canonical trace payload. Callers use this once, when a run first
// becomes eligible for evaluation, to obtain a TraceRef to pass around and
// later resolve via LoadTrace.
func (s *Store) TraceRefForRun(ctx context.Context, runID int64) (evaluation.TraceRef, error) {
	trace, err := s.loadTrace(ctx, runID)
	if err != nil {
		return evaluation.TraceRef{}, err
	}
	return trace.Ref, nil
}

// LoadTrace implements evaluation.TraceSource. It independently
// reconstructs the same canonical payload TraceRefForRun would produce for
// ref.RunID and verifies the resulting hash still matches ref.TraceHash,
// so a caller holding a stale or tampered ref gets a clear error instead
// of silently mismatched evidence.
func (s *Store) LoadTrace(ctx context.Context, ref evaluation.TraceRef) (evaluation.EvaluationTrace, error) {
	if err := ref.Validate(); err != nil {
		return evaluation.EvaluationTrace{}, err
	}
	if ref.OrganizationID != s.organizationID {
		return evaluation.EvaluationTrace{}, fmt.Errorf("%w: trace organization %q, store organization %q", ErrOrganizationMismatch, ref.OrganizationID, s.organizationID)
	}
	trace, err := s.loadTrace(ctx, ref.RunID)
	if err != nil {
		return evaluation.EvaluationTrace{}, err
	}
	if trace.Ref != ref {
		return evaluation.EvaluationTrace{}, fmt.Errorf("%w: run %d", evaluation.ErrTraceHashMismatch, ref.RunID)
	}
	return trace, nil
}

func (s *Store) loadTrace(ctx context.Context, runID int64) (evaluation.EvaluationTrace, error) {
	if runID <= 0 {
		return evaluation.EvaluationTrace{}, fmt.Errorf("%w: run id must be positive", ErrInvalidRun)
	}

	var taskID, attemptID int64
	var terminalReasonCode *string
	if err := s.pool.QueryRow(ctx, `
SELECT task_id, attempt_id, terminal_reason_code
FROM decision_graph_runs
WHERE id=$1 AND organization_id=$2 AND status='succeeded'`,
		runID, s.organizationID,
	).Scan(&taskID, &attemptID, &terminalReasonCode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return evaluation.EvaluationTrace{}, fmt.Errorf("%w: run %d", ErrRunNotSucceeded, runID)
		}
		return evaluation.EvaluationTrace{}, fmt.Errorf("decisiongraphtrace: load run %d: %w", runID, err)
	}

	var graphVersionID int64
	if err := s.pool.QueryRow(ctx, `
SELECT id FROM decision_graph_versions
WHERE run_id=$1 AND organization_id=$2
ORDER BY version_number DESC
LIMIT 1`, runID, s.organizationID).Scan(&graphVersionID); err != nil {
		return evaluation.EvaluationTrace{}, fmt.Errorf("decisiongraphtrace: load graph version for run %d: %w", runID, err)
	}

	nodes, err := s.loadNodes(ctx, runID, graphVersionID)
	if err != nil {
		return evaluation.EvaluationTrace{}, err
	}
	edges, err := s.loadEdges(ctx, runID, graphVersionID)
	if err != nil {
		return evaluation.EvaluationTrace{}, err
	}
	decision, err := s.loadDecision(ctx, runID)
	if err != nil {
		return evaluation.EvaluationTrace{}, err
	}

	payload := canonicalTrace{
		SchemaVersion:      schemaVersion,
		RunID:              runID,
		OrganizationID:     s.organizationID,
		TaskID:             taskID,
		AttemptID:          attemptID,
		TerminalReasonCode: terminalReasonCode,
		Nodes:              nodes,
		Edges:              edges,
		Decision:           decision,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return evaluation.EvaluationTrace{}, fmt.Errorf("decisiongraphtrace: marshal canonical trace for run %d: %w", runID, err)
	}
	sum := sha256.Sum256(body)

	trace := evaluation.EvaluationTrace{
		Ref: evaluation.TraceRef{
			RunID:          runID,
			TraceHash:      hex.EncodeToString(sum[:]),
			SchemaVersion:  schemaVersion,
			OrganizationID: s.organizationID,
		},
		Payload:  body,
		LoadedAt: time.Now().UTC(),
	}
	if err := trace.Validate(); err != nil {
		return evaluation.EvaluationTrace{}, fmt.Errorf("decisiongraphtrace: built an invalid trace for run %d: %w", runID, err)
	}
	return trace, nil
}

func (s *Store) loadNodes(ctx context.Context, runID, graphVersionID int64) ([]canonicalNode, error) {
	rows, err := s.pool.Query(ctx, `
SELECT logical_node_id, node_type, branch_state, execution_state, depth, payload_hash
FROM decision_graph_nodes
WHERE run_id=$1 AND organization_id=$2 AND graph_version_id=$3`,
		runID, s.organizationID, graphVersionID)
	if err != nil {
		return nil, fmt.Errorf("decisiongraphtrace: load nodes for run %d: %w", runID, err)
	}
	defer rows.Close()

	var nodes []canonicalNode
	for rows.Next() {
		var n canonicalNode
		if err := rows.Scan(&n.LogicalNodeID, &n.NodeType, &n.BranchState, &n.ExecutionState, &n.Depth, &n.PayloadHash); err != nil {
			return nil, fmt.Errorf("decisiongraphtrace: scan node for run %d: %w", runID, err)
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("decisiongraphtrace: iterate nodes for run %d: %w", runID, err)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].LogicalNodeID < nodes[j].LogicalNodeID })
	return nodes, nil
}

func (s *Store) loadEdges(ctx context.Context, runID, graphVersionID int64) ([]canonicalEdge, error) {
	rows, err := s.pool.Query(ctx, `
SELECT f.logical_node_id, t.logical_node_id, e.edge_type
FROM decision_graph_edges e
JOIN decision_graph_nodes f ON f.id=e.from_node_id
JOIN decision_graph_nodes t ON t.id=e.to_node_id
WHERE e.run_id=$1 AND e.organization_id=$2 AND e.graph_version_id=$3`,
		runID, s.organizationID, graphVersionID)
	if err != nil {
		return nil, fmt.Errorf("decisiongraphtrace: load edges for run %d: %w", runID, err)
	}
	defer rows.Close()

	var edges []canonicalEdge
	for rows.Next() {
		var e canonicalEdge
		if err := rows.Scan(&e.FromLogicalNodeID, &e.ToLogicalNodeID, &e.EdgeType); err != nil {
			return nil, fmt.Errorf("decisiongraphtrace: scan edge for run %d: %w", runID, err)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("decisiongraphtrace: iterate edges for run %d: %w", runID, err)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromLogicalNodeID != edges[j].FromLogicalNodeID {
			return edges[i].FromLogicalNodeID < edges[j].FromLogicalNodeID
		}
		if edges[i].ToLogicalNodeID != edges[j].ToLogicalNodeID {
			return edges[i].ToLogicalNodeID < edges[j].ToLogicalNodeID
		}
		return edges[i].EdgeType < edges[j].EdgeType
	})
	return edges, nil
}

func (s *Store) loadDecision(ctx context.Context, runID int64) (canonicalDecision, error) {
	var decision canonicalDecision
	if err := s.pool.QueryRow(ctx, `
SELECT n.logical_node_id, d.decision_hash, d.verification_label
FROM decision_records d
JOIN decision_graph_nodes n ON n.id=d.selected_candidate_node_id
WHERE d.run_id=$1 AND d.organization_id=$2`,
		runID, s.organizationID,
	).Scan(&decision.SelectedCandidateLogicalNodeID, &decision.DecisionHash, &decision.VerificationLabel); err != nil {
		return canonicalDecision{}, fmt.Errorf("decisiongraphtrace: load decision record for run %d: %w", runID, err)
	}
	return decision, nil
}
