package decisiongraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalGraph struct {
	SchemaVersion string        `json:"schema_version"`
	Nodes         []Node        `json:"nodes"`
	Edges         []Edge        `json:"edges"`
	Depths        map[int64]int `json:"depths"`
}

// CanonicalHash commits to depth alongside nodes and edges. Depth is always
// derived from the edge structure (see Graph.Depths), never a separately
// stored value, so it cannot drift from what nodes/edges already imply —
// but hashing it explicitly still means any consumer of this hash gets a
// complete commitment to the graph without having to reason about whether
// depth is covered.
func (g *Graph) CanonicalHash() (string, error) {
	depths, _, err := g.Depths()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(canonicalGraph{
		SchemaVersion: "decision-graph-v1",
		Nodes:         g.Nodes(),
		Edges:         g.Edges(),
		Depths:        depths,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
