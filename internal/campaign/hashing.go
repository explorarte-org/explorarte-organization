package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CanonicalPayload contains the semantic content of a proposal used to compute
// its canonical content hash.
type CanonicalPayload struct {
	Title              string                `json:"title"`
	Goal               string                `json:"goal"`
	AcceptanceCriteria []string              `json:"acceptance_criteria"`
	Requirements       []ProposalRequirement `json:"requirements"`
	Budget             *ProposalBudget       `json:"budget,omitempty"`
	Assumptions        []string              `json:"assumptions"`
	Risks              []string              `json:"risks"`
	OpenQuestions      []string              `json:"open_questions"`
}

// ComputeCanonicalHash computes a deterministic SHA-256 hex digest of the canonical proposal payload.
func ComputeCanonicalHash(payload CanonicalPayload) (string, error) {
	if payload.AcceptanceCriteria == nil {
		payload.AcceptanceCriteria = []string{}
	}
	if payload.Requirements == nil {
		payload.Requirements = []ProposalRequirement{}
	}
	if payload.Assumptions == nil {
		payload.Assumptions = []string{}
	}
	if payload.Risks == nil {
		payload.Risks = []string{}
	}
	if payload.OpenQuestions == nil {
		payload.OpenQuestions = []string{}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal canonical proposal payload: %w", err)
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
