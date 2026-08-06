package decisiongraph

import "fmt"

type DecisionRecord struct {
	DecisionNodeID          int64
	SelectedCandidateNodeID int64
	EvidenceNodeIDs         []int64
	VerificationNodeIDs     []int64
	Label                   VerificationLabel
}

func (d DecisionRecord) Validate(graph *Graph) error {
	if graph == nil || d.DecisionNodeID <= 0 || d.SelectedCandidateNodeID <= 0 || !d.Label.Valid() {
		return ErrInvalidDecision
	}
	decisionNode, ok := graph.nodes[d.DecisionNodeID]
	if !ok || decisionNode.Type != NodeDecision {
		return fmt.Errorf("%w: invalid decision node", ErrInvalidDecision)
	}
	candidate, ok := graph.nodes[d.SelectedCandidateNodeID]
	if !ok || candidate.Type != NodeCandidateAction || candidate.BranchState != BranchSelected {
		return fmt.Errorf("%w: invalid selected candidate", ErrInvalidDecision)
	}
	if err := validateTypedReferences(graph, d.EvidenceNodeIDs, NodeEvidence, "evidence"); err != nil {
		return err
	}
	if err := validateTypedReferences(graph, d.VerificationNodeIDs, NodeVerification, "verification"); err != nil {
		return err
	}
	return nil
}

func validateTypedReferences(graph *Graph, ids []int64, expected NodeType, label string) error {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("%w: invalid %s id", ErrInvalidDecision, label)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: duplicate %s id %d", ErrInvalidDecision, label, id)
		}
		seen[id] = struct{}{}
		node, ok := graph.nodes[id]
		if !ok || node.Type != expected {
			return fmt.Errorf("%w: invalid %s node %d", ErrInvalidDecision, label, id)
		}
	}
	return nil
}
