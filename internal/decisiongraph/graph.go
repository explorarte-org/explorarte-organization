package decisiongraph

import (
	"fmt"
	"sort"
)

type Graph struct {
	nodes map[int64]Node
	edges []Edge
}

func NewGraph(nodes []Node, edges []Edge) (*Graph, error) {
	g := &Graph{nodes: make(map[int64]Node, len(nodes)), edges: append([]Edge(nil), edges...)}
	goalCount := 0
	for _, node := range nodes {
		if err := node.Validate(); err != nil {
			return nil, err
		}
		if _, exists := g.nodes[node.ID]; exists {
			return nil, fmt.Errorf("%w: %d", ErrDuplicateNode, node.ID)
		}
		g.nodes[node.ID] = node
		if node.Type == NodeGoal {
			goalCount++
		}
	}
	if goalCount != 1 {
		return nil, fmt.Errorf("%w: expected exactly one goal, got %d", ErrInvalidGraph, goalCount)
	}
	seenEdges := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if err := edge.Validate(); err != nil {
			return nil, err
		}
		if _, ok := g.nodes[edge.FromNodeID]; !ok {
			return nil, fmt.Errorf("%w: edge source %d", ErrUnknownNode, edge.FromNodeID)
		}
		if _, ok := g.nodes[edge.ToNodeID]; !ok {
			return nil, fmt.Errorf("%w: edge target %d", ErrUnknownNode, edge.ToNodeID)
		}
		key := fmt.Sprintf("%d:%d:%s", edge.FromNodeID, edge.ToNodeID, edge.Type)
		if _, exists := seenEdges[key]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateEdge, key)
		}
		seenEdges[key] = struct{}{}
	}
	if g.hasDependencyCycle() {
		return nil, ErrDependencyCycle
	}
	return g, nil
}

func (g *Graph) Nodes() []Node {
	result := make([]Node, 0, len(g.nodes))
	for _, node := range g.nodes {
		result = append(result, node)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (g *Graph) Edges() []Edge {
	result := append([]Edge(nil), g.edges...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].FromNodeID != result[j].FromNodeID {
			return result[i].FromNodeID < result[j].FromNodeID
		}
		if result[i].ToNodeID != result[j].ToNodeID {
			return result[i].ToNodeID < result[j].ToNodeID
		}
		return result[i].Type < result[j].Type
	})
	return result
}

// ReadyNodeIDs returns active, pending nodes whose depends_on targets have all
// succeeded. Work discovery is optimistic; the durable store must revalidate
// eligibility and budget while claiming an execution.
func (g *Graph) ReadyNodeIDs() []int64 {
	dependencies := make(map[int64][]int64)
	for _, edge := range g.edges {
		if edge.Type == EdgeDependsOn {
			dependencies[edge.FromNodeID] = append(dependencies[edge.FromNodeID], edge.ToNodeID)
		}
	}
	result := make([]int64, 0)
	for id, node := range g.nodes {
		if node.BranchState != BranchActive || node.ExecutionState != ExecutionPending {
			continue
		}
		ready := true
		for _, dependencyID := range dependencies[id] {
			if g.nodes[dependencyID].ExecutionState != ExecutionSucceeded {
				ready = false
				break
			}
		}
		if ready {
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (g *Graph) hasDependencyCycle() bool {
	adjacency := make(map[int64][]int64)
	for _, edge := range g.edges {
		if edge.Type == EdgeDependsOn {
			// From depends on To, so To must execute before From.
			adjacency[edge.ToNodeID] = append(adjacency[edge.ToNodeID], edge.FromNodeID)
		}
	}
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[int64]int, len(g.nodes))
	var visit func(int64) bool
	visit = func(id int64) bool {
		if state[id] == visiting {
			return true
		}
		if state[id] == visited {
			return false
		}
		state[id] = visiting
		for _, next := range adjacency[id] {
			if visit(next) {
				return true
			}
		}
		state[id] = visited
		return false
	}
	for id := range g.nodes {
		if state[id] == unvisited && visit(id) {
			return true
		}
	}
	return false
}
