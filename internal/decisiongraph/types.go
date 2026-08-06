package decisiongraph

import (
	"fmt"
	"regexp"
	"strings"
)

type NodeType string

const (
	NodeGoal            NodeType = "goal"
	NodeRequirement     NodeType = "requirement"
	NodeConstraint      NodeType = "constraint"
	NodeHypothesis      NodeType = "hypothesis"
	NodeCandidateAction NodeType = "candidate_action"
	NodeEvidence        NodeType = "evidence"
	NodeVerification    NodeType = "verification"
	NodeDecision        NodeType = "decision"
)

type EdgeType string

const (
	EdgeDependsOn    EdgeType = "depends_on"
	EdgeSupports     EdgeType = "supports"
	EdgeContradicts  EdgeType = "contradicts"
	EdgeSatisfies    EdgeType = "satisfies"
	EdgePrunes       EdgeType = "prunes"
	EdgeSelectedFrom EdgeType = "selected_from"
)

type BranchState string

const (
	BranchActive               BranchState = "active"
	BranchSelected             BranchState = "selected"
	BranchRejectedByEvidence   BranchState = "rejected_by_evidence"
	BranchRejectedByPolicy     BranchState = "rejected_by_policy"
	BranchRejectedByCapability BranchState = "rejected_by_capability"
	BranchRejectedByDependency BranchState = "rejected_by_dependency"
	BranchRejectedByBudget     BranchState = "rejected_by_budget"
	BranchSuperseded           BranchState = "superseded"
	BranchInconclusive         BranchState = "inconclusive"
)

type ExecutionState string

const (
	ExecutionPending             ExecutionState = "pending"
	ExecutionReady               ExecutionState = "ready"
	ExecutionRunning             ExecutionState = "running"
	ExecutionWaitingVerification ExecutionState = "waiting_verification"
	ExecutionSucceeded           ExecutionState = "succeeded"
	ExecutionFailed              ExecutionState = "failed"
	ExecutionCancelled           ExecutionState = "cancelled"
	ExecutionAmbiguous           ExecutionState = "ambiguous"
)

type VerificationLabel string

const (
	VerificationVerified     VerificationLabel = "verified"
	VerificationInferred     VerificationLabel = "inferred"
	VerificationUnknown      VerificationLabel = "unknown"
	VerificationContradicted VerificationLabel = "contradicted"
)

type RunStatus string

const (
	RunPlanned   RunStatus = "planned"
	RunRunning   RunStatus = "running"
	RunWaiting   RunStatus = "waiting"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
	RunAmbiguous RunStatus = "ambiguous"
)

type Node struct {
	ID                   int64
	Type                 NodeType
	BranchState          BranchState
	ExecutionState       ExecutionState
	PayloadSchemaVersion string
	PayloadHash          string
	ContextSnapshotID    *int64
	CreatedBy            string
}

type Edge struct {
	FromNodeID int64
	ToNodeID   int64
	Type       EdgeType
}

var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (n Node) Validate() error {
	if n.ID <= 0 || !n.Type.Valid() || !n.BranchState.Valid() || !n.ExecutionState.Valid() {
		return fmt.Errorf("%w: invalid node identity or enum", ErrInvalidGraph)
	}
	if strings.TrimSpace(n.PayloadSchemaVersion) == "" || len(n.PayloadSchemaVersion) > 120 {
		return fmt.Errorf("%w: invalid payload schema version", ErrInvalidGraph)
	}
	if !sha256HexPattern.MatchString(n.PayloadHash) {
		return fmt.Errorf("%w: invalid payload hash", ErrInvalidGraph)
	}
	if n.ContextSnapshotID != nil && *n.ContextSnapshotID <= 0 {
		return fmt.Errorf("%w: invalid context snapshot id", ErrInvalidGraph)
	}
	if strings.TrimSpace(n.CreatedBy) == "" || len(n.CreatedBy) > 200 {
		return fmt.Errorf("%w: invalid created-by identity", ErrInvalidGraph)
	}
	return nil
}

func (e Edge) Validate() error {
	if e.FromNodeID <= 0 || e.ToNodeID <= 0 || e.FromNodeID == e.ToNodeID || !e.Type.Valid() {
		return fmt.Errorf("%w: invalid edge", ErrInvalidGraph)
	}
	return nil
}

func (v NodeType) Valid() bool {
	switch v {
	case NodeGoal, NodeRequirement, NodeConstraint, NodeHypothesis, NodeCandidateAction, NodeEvidence, NodeVerification, NodeDecision:
		return true
	default:
		return false
	}
}

func (v EdgeType) Valid() bool {
	switch v {
	case EdgeDependsOn, EdgeSupports, EdgeContradicts, EdgeSatisfies, EdgePrunes, EdgeSelectedFrom:
		return true
	default:
		return false
	}
}

func (v BranchState) Valid() bool {
	switch v {
	case BranchActive, BranchSelected, BranchRejectedByEvidence, BranchRejectedByPolicy, BranchRejectedByCapability, BranchRejectedByDependency, BranchRejectedByBudget, BranchSuperseded, BranchInconclusive:
		return true
	default:
		return false
	}
}

func (v ExecutionState) Valid() bool {
	switch v {
	case ExecutionPending, ExecutionReady, ExecutionRunning, ExecutionWaitingVerification, ExecutionSucceeded, ExecutionFailed, ExecutionCancelled, ExecutionAmbiguous:
		return true
	default:
		return false
	}
}

func (v VerificationLabel) Valid() bool {
	switch v {
	case VerificationVerified, VerificationInferred, VerificationUnknown, VerificationContradicted:
		return true
	default:
		return false
	}
}

func (v RunStatus) Valid() bool {
	switch v {
	case RunPlanned, RunRunning, RunWaiting, RunSucceeded, RunFailed, RunCancelled, RunAmbiguous:
		return true
	default:
		return false
	}
}
