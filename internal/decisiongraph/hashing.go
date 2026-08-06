package decisiongraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalGraph struct {
	SchemaVersion string `json:"schema_version"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

func (g *Graph) CanonicalHash() (string, error) {
	body, err := json.Marshal(canonicalGraph{
		SchemaVersion: "decision-graph-v1",
		Nodes:         g.Nodes(),
		Edges:         g.Edges(),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// HashGraph preserves the original package-level API used by the durable
// PostgreSQL ledger bundle while delegating to the canonical method. New code
// should prefer (*Graph).CanonicalHash directly.
func HashGraph(graph *Graph) (string, error) {
	return graph.CanonicalHash()
}
