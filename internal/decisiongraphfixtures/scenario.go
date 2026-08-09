// Package decisiongraphfixtures is the dedicated bridge between R30's
// evaluation fixtures (internal/evaluation/fixtures, a pure package with no
// decisiongraph dependency by design — see scripts/check-improvement-
// fitness.sh, which enforces that internal/evaluation must stay decoupled
// from internal/decisiongraph's Go API) and internal/decisiongraph's own
// pure, in-memory domain (Graph, BudgetLimits, DecisionRecord). Only this
// package imports both; internal/evaluation/fixtures itself never does.
package decisiongraphfixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
)

// DecisionGraphScenario is the Scenario payload DecisionGraphRunner
// expects. It exercises internal/decisiongraph's pure, in-memory domain
// layer directly — no Postgres dependency — which is exactly what R30's
// DAG/budget/evidence fixtures need to be reproducible without external
// state.
type DecisionGraphScenario struct {
	// AcyclicNodes/AcyclicEdges must build a valid *decisiongraph.Graph.
	AcyclicNodes []decisiongraph.Node
	AcyclicEdges []decisiongraph.Edge
	// ExpectedMaxDepth is the longest goal-distance the fixture expects
	// Graph.Depths() to report for AcyclicNodes/AcyclicEdges.
	ExpectedMaxDepth int
	// CycleEdges, appended to AcyclicEdges, must make NewGraph fail with
	// ErrDependencyCycle. Nil skips this check.
	CycleEdges []decisiongraph.Edge

	// TerminalDecision, if set, is checked against the same
	// verified-or-inferred rule internal/decisiongraph's Service enforces
	// (service.go: "terminal selection requires verified or inferred
	// label") — independently re-derived here, not imported, because this
	// evaluation harness exists to catch exactly this class of regression
	// even if the library's own check were ever weakened.
	TerminalDecision           *decisiongraph.DecisionRecord
	TerminalDecisionShouldPass bool

	// Budget, if set, drives a sequence of BudgetLimits.Reserve calls
	// (BudgetDeltas, applied in order against a running BudgetUsage
	// starting at zero) and asserts the call at ExhaustingDeltaIndex (0
	// if unset, i.e. the first delta) is the first to fail with
	// ErrBudgetExceeded, and every prior delta succeeds cleanly.
	Budget               *decisiongraph.BudgetLimits
	BudgetDeltas         []decisiongraph.BudgetUsage
	ExhaustingDeltaIndex int
}

func hashOf(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func node(id int64, kind decisiongraph.NodeType, branch decisiongraph.BranchState, exec decisiongraph.ExecutionState, label string) decisiongraph.Node {
	return decisiongraph.Node{
		ID: id, Type: kind, BranchState: branch, ExecutionState: exec,
		PayloadSchemaVersion: "r30-fixture.v1", PayloadHash: hashOf(label), CreatedBy: "r30/evaluation-harness",
	}
}

func edge(from, to int64, kind decisiongraph.EdgeType) decisiongraph.Edge {
	return decisiongraph.Edge{FromNodeID: from, ToNodeID: to, Type: kind}
}

func budgetExhaustionScenario() *DecisionGraphScenario {
	limits := decisiongraph.BudgetLimits{MaxNodes: 100, MaxDepth: 20, MaxParallelNodes: 10, MaxModelCalls: 3, MaxInputTokens: 100000, MaxOutputTokens: 100000, MaxReplans: 5, MaxVerifications: 10, MaxWallTime: time.Hour}
	return &DecisionGraphScenario{
		AcyclicNodes: []decisiongraph.Node{
			node(1, decisiongraph.NodeGoal, decisiongraph.BranchActive, decisiongraph.ExecutionPending, "goal"),
		},
		AcyclicEdges:     nil,
		ExpectedMaxDepth: 0,
		Budget:           &limits,
		BudgetDeltas: []decisiongraph.BudgetUsage{
			{ModelCalls: 1, Nodes: 1, Depth: 0},
			{ModelCalls: 1, Nodes: 1, Depth: 0},
			{ModelCalls: 2, Nodes: 1, Depth: 0}, // 1+1+2=4 > MaxModelCalls=3, must fail here
		},
		ExhaustingDeltaIndex: 2,
	}
}

func dagCyclesDepthTerminalEvidenceScenario() *DecisionGraphScenario {
	nodes := []decisiongraph.Node{
		node(1, decisiongraph.NodeGoal, decisiongraph.BranchActive, decisiongraph.ExecutionPending, "goal"),
		node(2, decisiongraph.NodeRequirement, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded, "requirement"),
		node(3, decisiongraph.NodeCandidateAction, decisiongraph.BranchSelected, decisiongraph.ExecutionSucceeded, "candidate"),
		node(4, decisiongraph.NodeEvidence, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded, "evidence"),
		node(5, decisiongraph.NodeVerification, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded, "verification"),
		node(6, decisiongraph.NodeDecision, decisiongraph.BranchActive, decisiongraph.ExecutionPending, "decision"),
	}
	edges := []decisiongraph.Edge{
		edge(2, 1, decisiongraph.EdgeDependsOn),
		edge(3, 2, decisiongraph.EdgeDependsOn),
		edge(4, 3, decisiongraph.EdgeSupports),
		edge(5, 4, decisiongraph.EdgeSupports),
		edge(6, 2, decisiongraph.EdgeDependsOn),
	}
	terminal := decisiongraph.DecisionRecord{
		DecisionNodeID: 6, SelectedCandidateNodeID: 3, EvidenceNodeIDs: []int64{4}, VerificationNodeIDs: []int64{5},
		Label: decisiongraph.VerificationUnknown,
	}
	return &DecisionGraphScenario{
		AcyclicNodes: nodes, AcyclicEdges: edges, ExpectedMaxDepth: 4,
		CycleEdges:                 []decisiongraph.Edge{edge(1, 3, decisiongraph.EdgeDependsOn)},
		TerminalDecision:           &terminal,
		TerminalDecisionShouldPass: false,
	}
}

func contradictoryEvidenceNonSelectionScenario() *DecisionGraphScenario {
	nodes := []decisiongraph.Node{
		node(1, decisiongraph.NodeGoal, decisiongraph.BranchActive, decisiongraph.ExecutionPending, "goal"),
		node(2, decisiongraph.NodeCandidateAction, decisiongraph.BranchRejectedByEvidence, decisiongraph.ExecutionFailed, "candidate"),
		node(3, decisiongraph.NodeEvidence, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded, "contradicted-evidence"),
		node(4, decisiongraph.NodeDecision, decisiongraph.BranchActive, decisiongraph.ExecutionPending, "decision"),
	}
	edges := []decisiongraph.Edge{
		edge(2, 1, decisiongraph.EdgeDependsOn),
		edge(3, 2, decisiongraph.EdgeContradicts),
		edge(4, 1, decisiongraph.EdgeDependsOn),
	}
	terminal := decisiongraph.DecisionRecord{
		DecisionNodeID: 4, SelectedCandidateNodeID: 2, EvidenceNodeIDs: []int64{3},
		Label: decisiongraph.VerificationContradicted,
	}
	return &DecisionGraphScenario{
		AcyclicNodes: nodes, AcyclicEdges: edges, ExpectedMaxDepth: 2,
		TerminalDecision:           &terminal,
		TerminalDecisionShouldPass: false,
	}
}

// scenariosByFixtureID is the complete set of fixtures this package can
// activate — every key here corresponds to an ID in
// internal/evaluation/fixtures.CatalogR30.
func scenariosByFixtureID() map[string]*DecisionGraphScenario {
	return map[string]*DecisionGraphScenario{
		"r30-08-budget-exhaustion":                    budgetExhaustionScenario(),
		"r30-11-dag-cycles-depth-terminal-evidence":   dagCyclesDepthTerminalEvidenceScenario(),
		"r30-12-contradictory-evidence-non-selection": contradictoryEvidenceNonSelectionScenario(),
	}
}
