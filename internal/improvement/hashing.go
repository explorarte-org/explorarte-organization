package improvement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalCandidate struct {
	SchemaVersion string      `json:"schema_version"`
	Artifact      ArtifactRef `json:"artifact"`
	Lineage       Lineage     `json:"lineage"`
}

// CanonicalHash is the candidate's stable identity hash: the artifact it
// proposes plus its lineage, independent of ID, state or timestamps. Two
// candidates proposing the same artifact from the same lineage hash equal.
func (c Candidate) CanonicalHash() (string, error) {
	body, err := json.Marshal(canonicalCandidate{
		SchemaVersion: "bounded-self-improvement-candidate-v1",
		Artifact:      c.Artifact,
		Lineage:       c.Lineage,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
