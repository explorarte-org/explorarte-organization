package decisiongraph

import (
	"errors"
	"math"
	"testing"
	"time"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testNode(id int64, kind NodeType, branch BranchState, execution ExecutionState) Node {
	return Node{ID: id, Type: kind, BranchState: branch, ExecutionState: execution, PayloadSchemaVersion: "v1", PayloadHash: testHash, CreatedBy: "planner"}
}

func TestGraphRejectsDependencyCycle(t *testing.T) {
	_, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionPending),
		testNode(2, NodeCandidateAction, BranchActive, ExecutionPending),
	}, []Edge{
		{FromNodeID: 1, ToNodeID: 2, Type: EdgeDependsOn},
		{FromNodeID: 2, ToNodeID: 1, Type: EdgeDependsOn},
	})
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected dependency cycle, got %v", err)
	}
}

func TestGraphAllowsNonSchedulingSemanticCycle(t *testing.T) {
	graph, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionPending),
		testNode(2, NodeEvidence, BranchActive, ExecutionPending),
	}, []Edge{
		{FromNodeID: 1, ToNodeID: 2, Type: EdgeSupports},
		{FromNodeID: 2, ToNodeID: 1, Type: EdgeContradicts},
	})
	if err != nil {
		t.Fatalf("semantic links must not define scheduler cycles: %v", err)
	}
	if graph == nil {
		t.Fatal("expected graph")
	}
}

func TestReadyNodeIDsRequireSucceededDependencies(t *testing.T) {
	graph, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeRequirement, BranchActive, ExecutionPending),
		testNode(3, NodeCandidateAction, BranchActive, ExecutionPending),
		testNode(4, NodeCandidateAction, BranchRejectedByPolicy, ExecutionPending),
	}, []Edge{
		{FromNodeID: 2, ToNodeID: 1, Type: EdgeDependsOn},
		{FromNodeID: 3, ToNodeID: 2, Type: EdgeDependsOn},
	})
	if err != nil {
		t.Fatal(err)
	}
	ready := graph.ReadyNodeIDs()
	if len(ready) != 1 || ready[0] != 2 {
		t.Fatalf("expected only node 2 ready, got %v", ready)
	}
}

func TestCanonicalHashIsOrderIndependent(t *testing.T) {
	nodes := []Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeCandidateAction, BranchActive, ExecutionPending),
	}
	edges := []Edge{{FromNodeID: 2, ToNodeID: 1, Type: EdgeDependsOn}}
	first, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewGraph([]Node{nodes[1], nodes[0]}, edges)
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := first.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := second.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("hashes differ: %s != %s", firstHash, secondHash)
	}
}

func TestGraphDepthsAreDerivedFromEdgeStructureNotCallerInput(t *testing.T) {
	graph, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeCandidateAction, BranchActive, ExecutionPending),
		testNode(3, NodeDecision, BranchActive, ExecutionPending),
	}, []Edge{
		{FromNodeID: 2, ToNodeID: 1, Type: EdgeSatisfies},
		{FromNodeID: 3, ToNodeID: 2, Type: EdgeDependsOn},
	})
	if err != nil {
		t.Fatal(err)
	}
	depths, maxDepth, err := graph.Depths()
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]int{1: 0, 2: 1, 3: 2}
	for id, depth := range want {
		if depths[id] != depth {
			t.Fatalf("node %d depth=%d want %d (depths=%v)", id, depths[id], depth, depths)
		}
	}
	if maxDepth != 2 {
		t.Fatalf("maxDepth=%d want 2", maxDepth)
	}
}

func TestGraphDepthsRejectsNodeDisconnectedFromGoal(t *testing.T) {
	graph, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeCandidateAction, BranchActive, ExecutionPending),
		testNode(3, NodeCandidateAction, BranchRejectedByPolicy, ExecutionPending),
	}, []Edge{
		{FromNodeID: 2, ToNodeID: 1, Type: EdgeSatisfies},
		// node 3 has no edge to the rest of the graph.
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := graph.Depths(); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a node disconnected from goal, got %v", err)
	}
}

func TestCanonicalHashChangesWithDepth(t *testing.T) {
	shallow, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeCandidateAction, BranchActive, ExecutionPending),
	}, []Edge{{FromNodeID: 2, ToNodeID: 1, Type: EdgeDependsOn}})
	if err != nil {
		t.Fatal(err)
	}
	deep, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeCandidateAction, BranchActive, ExecutionPending),
		testNode(3, NodeDecision, BranchActive, ExecutionPending),
	}, []Edge{
		{FromNodeID: 2, ToNodeID: 1, Type: EdgeDependsOn},
		{FromNodeID: 3, ToNodeID: 2, Type: EdgeDependsOn},
	})
	if err != nil {
		t.Fatal(err)
	}
	shallowHash, err := shallow.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	deepHash, err := deep.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	if shallowHash == deepHash {
		t.Fatal("hash must differ when depth structure differs")
	}
}

func TestRejectedBranchRequiresNewEvidenceToReopen(t *testing.T) {
	if err := ValidateBranchTransition(BranchRejectedByEvidence, BranchActive, false); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
	if err := ValidateBranchTransition(BranchRejectedByEvidence, BranchActive, true); err != nil {
		t.Fatalf("expected evidence reopen, got %v", err)
	}
}

func TestAmbiguousExecutionIsTerminal(t *testing.T) {
	if err := ValidateExecutionTransition(ExecutionAmbiguous, ExecutionReady); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ambiguous to be terminal, got %v", err)
	}
}

func testLimits() BudgetLimits {
	return BudgetLimits{
		MaxNodes: 10, MaxDepth: 5, MaxParallelNodes: 3, MaxModelCalls: 8,
		MaxInputTokens: 1000, MaxOutputTokens: 500, MaxReplans: 2,
		MaxVerifications: 6, MaxWallTime: time.Minute,
	}
}

func TestBudgetReserveIsAtomic(t *testing.T) {
	limits := testLimits()
	current := BudgetUsage{Nodes: 9, ModelCalls: 2}
	_, err := limits.Reserve(current, BudgetUsage{Nodes: 2, ModelCalls: 1})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected budget exceeded, got %v", err)
	}
	if current.Nodes != 9 || current.ModelCalls != 2 {
		t.Fatalf("current usage mutated: %+v", current)
	}
}

func TestBudgetReserveRejectsOverflow(t *testing.T) {
	limits := testLimits()
	limits.MaxNodes = math.MaxInt64
	_, err := limits.Reserve(BudgetUsage{Nodes: math.MaxInt64}, BudgetUsage{Nodes: 1})
	if !errors.Is(err, ErrBudgetOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}

func TestDecisionRequiresSelectedCandidateAndTypedEvidence(t *testing.T) {
	graph, err := NewGraph([]Node{
		testNode(1, NodeGoal, BranchActive, ExecutionSucceeded),
		testNode(2, NodeCandidateAction, BranchSelected, ExecutionSucceeded),
		testNode(3, NodeEvidence, BranchActive, ExecutionSucceeded),
		testNode(4, NodeVerification, BranchActive, ExecutionSucceeded),
		testNode(5, NodeDecision, BranchActive, ExecutionSucceeded),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	record := DecisionRecord{
		DecisionNodeID: 5, SelectedCandidateNodeID: 2,
		EvidenceNodeIDs: []int64{3}, VerificationNodeIDs: []int64{4},
		Label: VerificationVerified,
	}
	if err := record.Validate(graph); err != nil {
		t.Fatalf("expected valid decision, got %v", err)
	}
}
