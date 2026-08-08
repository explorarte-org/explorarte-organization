package decisiongraph

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Run struct {
	ID                           int64
	OrganizationID               string
	TaskID                       int64
	AttemptID                    int64
	Status                       RunStatus
	ReasoningPolicySchemaVersion string
	ReasoningPolicyHash          string
	IdempotencyKey               string
	BudgetLimits                 BudgetLimits
	Deadline                     time.Time
	CreatedBy                    string
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	TerminalAt                   *time.Time
}

type CreateRunRequest struct {
	TaskID                       int64
	AttemptID                    int64
	ReasoningPolicySchemaVersion string
	ReasoningPolicyHash          string
	IdempotencyKey               string
	BudgetLimits                 BudgetLimits
	Deadline                     time.Time
	CreatedBy                    string
}

func (r CreateRunRequest) Validate(now time.Time) error {
	if r.TaskID <= 0 || r.AttemptID <= 0 {
		return fmt.Errorf("%w: invalid task or attempt", ErrInvalidRun)
	}
	if strings.TrimSpace(r.ReasoningPolicySchemaVersion) == "" || len(r.ReasoningPolicySchemaVersion) > 120 {
		return fmt.Errorf("%w: invalid reasoning policy schema version", ErrInvalidRun)
	}
	if !sha256HexPattern.MatchString(r.ReasoningPolicyHash) {
		return fmt.Errorf("%w: invalid reasoning policy hash", ErrInvalidRun)
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" || len(r.IdempotencyKey) > 200 {
		return fmt.Errorf("%w: invalid idempotency key", ErrInvalidRun)
	}
	if err := r.BudgetLimits.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRun, err)
	}
	if !r.Deadline.After(now) {
		return fmt.Errorf("%w: deadline must be in the future", ErrInvalidRun)
	}
	if strings.TrimSpace(r.CreatedBy) == "" || len(r.CreatedBy) > 200 {
		return fmt.Errorf("%w: invalid created-by identity", ErrInvalidRun)
	}
	return nil
}

type GraphVersion struct {
	ID            int64
	RunID         int64
	VersionNumber int
	SnapshotHash  string
	NodeCount     int
	MaxDepth      int
	CreatedBy     string
	CreatedAt     time.Time
	// NodeIDs maps each AppendGraphRequest.Nodes[i].ID (the caller-chosen
	// logical_node_id) to the durable-store-assigned node id that
	// RecordVerification, TransitionBranch and RecordTerminalDecision
	// require. Callers that only append a graph and never verify/decide
	// against it may ignore this field.
	NodeIDs map[int64]int64
}

type AppendGraphRequest struct {
	RunID     int64
	Nodes     []Node
	Edges     []Edge
	Depths    map[int64]int
	CreatedBy string
}

func (r AppendGraphRequest) Validate() (*Graph, string, int, error) {
	if r.RunID <= 0 {
		return nil, "", 0, fmt.Errorf("%w: invalid run id", ErrInvalidGraph)
	}
	if strings.TrimSpace(r.CreatedBy) == "" || len(r.CreatedBy) > 200 {
		return nil, "", 0, fmt.Errorf("%w: invalid created-by identity", ErrInvalidGraph)
	}
	graph, err := NewGraph(r.Nodes, r.Edges)
	if err != nil {
		return nil, "", 0, err
	}
	maxDepth := 0
	for _, node := range r.Nodes {
		depth, ok := r.Depths[node.ID]
		if !ok || depth < 0 {
			return nil, "", 0, fmt.Errorf("%w: missing or invalid depth for node %d", ErrInvalidGraph, node.ID)
		}
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	if len(r.Depths) != len(r.Nodes) {
		return nil, "", 0, fmt.Errorf("%w: depth map contains unknown nodes", ErrInvalidGraph)
	}
	hash, err := graph.CanonicalHash()
	if err != nil {
		return nil, "", 0, err
	}
	return graph, hash, maxDepth, nil
}

type NodeClaim struct {
	ExecutionID    int64
	RunID          int64
	GraphVersionID int64
	NodeID         int64
	LogicalNodeID  int64
	NodeType       NodeType
	ClaimToken     string
	ClaimExpiresAt time.Time
}

type ClaimNodeRequest struct {
	RunID         int64
	ClaimedBy     string
	LeaseDuration time.Duration
}

func (r ClaimNodeRequest) Validate() error {
	if r.RunID <= 0 {
		return fmt.Errorf("%w: invalid run id", ErrInvalidClaim)
	}
	if strings.TrimSpace(r.ClaimedBy) == "" || len(r.ClaimedBy) > 200 {
		return fmt.Errorf("%w: invalid claimant", ErrInvalidClaim)
	}
	if r.LeaseDuration <= 0 || r.LeaseDuration > 24*time.Hour {
		return fmt.Errorf("%w: invalid lease duration", ErrInvalidClaim)
	}
	return nil
}

type FinishExecutionRequest struct {
	ExecutionID       int64
	ClaimToken        string
	FinalState        ExecutionState
	InputTokens       int64
	OutputTokens      int64
	OutcomeHash       string
	ReasonCode        string
	ModelInvocationID *int64
	DispatchAttemptID *int64
}

func (r FinishExecutionRequest) Validate() error {
	if r.ExecutionID <= 0 || strings.TrimSpace(r.ClaimToken) == "" {
		return fmt.Errorf("%w: missing execution identity", ErrInvalidExecution)
	}
	switch r.FinalState {
	case ExecutionSucceeded, ExecutionFailed, ExecutionCancelled, ExecutionAmbiguous, ExecutionWaitingVerification:
	default:
		return fmt.Errorf("%w: invalid final state", ErrInvalidExecution)
	}
	if r.InputTokens < 0 || r.OutputTokens < 0 {
		return fmt.Errorf("%w: negative token usage", ErrInvalidExecution)
	}
	if r.OutcomeHash != "" && !sha256HexPattern.MatchString(r.OutcomeHash) {
		return fmt.Errorf("%w: invalid outcome hash", ErrInvalidExecution)
	}
	if len(r.ReasonCode) > 120 {
		return fmt.Errorf("%w: reason code too long", ErrInvalidExecution)
	}
	if (r.ModelInvocationID == nil) != (r.DispatchAttemptID == nil) {
		return fmt.Errorf("%w: invocation and dispatch attempt must be supplied together", ErrInvalidExecution)
	}
	if r.ModelInvocationID != nil && (*r.ModelInvocationID <= 0 || *r.DispatchAttemptID <= 0) {
		return fmt.Errorf("%w: invalid runtime linkage", ErrInvalidExecution)
	}
	return nil
}

type ObservationRecord struct {
	ExecutionID         int64
	SchemaVersion       string
	ObservationHash     string
	SourceKind          string
	SourceReferenceHash string
}

func (r ObservationRecord) Validate() error {
	if r.ExecutionID <= 0 || strings.TrimSpace(r.SchemaVersion) == "" || len(r.SchemaVersion) > 120 {
		return fmt.Errorf("%w: invalid observation identity", ErrInvalidObservation)
	}
	if !sha256HexPattern.MatchString(r.ObservationHash) {
		return fmt.Errorf("%w: invalid observation hash", ErrInvalidObservation)
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`).MatchString(r.SourceKind) {
		return fmt.Errorf("%w: invalid source kind", ErrInvalidObservation)
	}
	if r.SourceReferenceHash != "" && !sha256HexPattern.MatchString(r.SourceReferenceHash) {
		return fmt.Errorf("%w: invalid source reference hash", ErrInvalidObservation)
	}
	return nil
}

type VerificationRecord struct {
	RunID           int64
	NodeID          int64
	ExecutionID     *int64
	Label           VerificationLabel
	VerifierRef     string
	VerifierVersion string
	EvidenceSetHash string
	ReasonCodes     []string
}

func (r VerificationRecord) Validate() error {
	if r.RunID <= 0 || r.NodeID <= 0 || !r.Label.Valid() {
		return fmt.Errorf("%w: invalid verification identity", ErrInvalidVerification)
	}
	if r.ExecutionID != nil && *r.ExecutionID <= 0 {
		return fmt.Errorf("%w: invalid execution id", ErrInvalidVerification)
	}
	if strings.TrimSpace(r.VerifierRef) == "" || len(r.VerifierRef) > 200 || strings.TrimSpace(r.VerifierVersion) == "" || len(r.VerifierVersion) > 120 {
		return fmt.Errorf("%w: invalid verifier", ErrInvalidVerification)
	}
	if !sha256HexPattern.MatchString(r.EvidenceSetHash) {
		return fmt.Errorf("%w: invalid evidence set hash", ErrInvalidVerification)
	}
	for _, code := range r.ReasonCodes {
		if !regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,119}$`).MatchString(code) {
			return fmt.Errorf("%w: invalid reason code", ErrInvalidVerification)
		}
	}
	return nil
}

type TerminalDecisionRequest struct {
	RunID                   int64
	DecisionNodeID          int64
	SelectedCandidateNodeID int64
	EvidenceSetHash         string
	VerificationSetHash     string
	DecisionHash            string
	VerificationLabel       VerificationLabel
	CreatedBy               string
}

func (r TerminalDecisionRequest) Validate() error {
	if r.RunID <= 0 || r.DecisionNodeID <= 0 || r.SelectedCandidateNodeID <= 0 {
		return fmt.Errorf("%w: invalid decision identity", ErrInvalidDecision)
	}
	for _, hash := range []string{r.EvidenceSetHash, r.VerificationSetHash, r.DecisionHash} {
		if !sha256HexPattern.MatchString(hash) {
			return fmt.Errorf("%w: invalid decision hash", ErrInvalidDecision)
		}
	}
	if !r.VerificationLabel.Valid() {
		return fmt.Errorf("%w: invalid verification label", ErrInvalidDecision)
	}
	if strings.TrimSpace(r.CreatedBy) == "" || len(r.CreatedBy) > 200 {
		return fmt.Errorf("%w: invalid created-by identity", ErrInvalidDecision)
	}
	return nil
}

// CloseUnselectedRunRequest terminates a run whose candidate action was
// rejected by evidence or left inconclusive — the completion verdict was
// fail or inconclusive, so there is no terminal *selection* to record via
// RecordTerminalDecision (that requires a verified or inferred label). Without
// this, such runs never leave 'running': the task attempt is finished, but
// the decision graph never learns that.
type CloseUnselectedRunRequest struct {
	RunID           int64
	DecisionNodeID  int64
	CandidateNodeID int64
	Status          RunStatus
	BranchState     BranchState
	ReasonCode      string
	CreatedBy       string
}

func (r CloseUnselectedRunRequest) Validate() error {
	if r.RunID <= 0 || r.DecisionNodeID <= 0 || r.CandidateNodeID <= 0 {
		return fmt.Errorf("%w: invalid run/node identity", ErrInvalidDecision)
	}
	if r.Status != RunFailed && r.Status != RunAmbiguous {
		return fmt.Errorf("%w: closing an unselected run requires a failed or ambiguous status", ErrInvalidDecision)
	}
	if r.BranchState != BranchRejectedByEvidence && r.BranchState != BranchInconclusive {
		return fmt.Errorf("%w: closing an unselected run requires a rejected or inconclusive branch state", ErrInvalidDecision)
	}
	if strings.TrimSpace(r.ReasonCode) == "" || len(r.ReasonCode) > 120 {
		return fmt.Errorf("%w: invalid reason code", ErrInvalidDecision)
	}
	if strings.TrimSpace(r.CreatedBy) == "" || len(r.CreatedBy) > 200 {
		return fmt.Errorf("%w: invalid created-by identity", ErrInvalidDecision)
	}
	return nil
}

type BranchTransitionRequest struct {
	RunID        int64
	NodeID       int64
	ToState      BranchState
	EvidenceHash string
	ReasonCode   string
	Actor        string
}

func (r BranchTransitionRequest) Validate() error {
	if r.RunID <= 0 || r.NodeID <= 0 || !r.ToState.Valid() {
		return fmt.Errorf("%w: invalid branch transition identity", ErrInvalidTransition)
	}
	if r.ToState == BranchActive && !sha256HexPattern.MatchString(r.EvidenceHash) {
		return fmt.Errorf("%w: reopening requires an evidence hash", ErrInvalidTransition)
	}
	if r.EvidenceHash != "" && !sha256HexPattern.MatchString(r.EvidenceHash) {
		return fmt.Errorf("%w: invalid evidence hash", ErrInvalidTransition)
	}
	if r.ReasonCode != "" && !regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,119}$`).MatchString(r.ReasonCode) {
		return fmt.Errorf("%w: invalid branch reason code", ErrInvalidTransition)
	}
	if strings.TrimSpace(r.Actor) == "" || len(r.Actor) > 200 {
		return fmt.Errorf("%w: invalid branch actor", ErrInvalidTransition)
	}
	return nil
}

type TraceRef struct {
	OrganizationID string
	RunID          int64
	TraceHash      string
	SchemaVersion  string
}
