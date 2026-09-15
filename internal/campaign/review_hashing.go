package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ReviewCanonicalPayload contains the semantic content of a financial review
// used to compute its canonical content hash.
type ReviewCanonicalPayload struct {
	ProposalID            int64                  `json:"proposal_id"`
	ProposalCanonicalHash string                 `json:"proposal_canonical_hash"`
	ReviewerRoleID        string                 `json:"reviewer_role_id"`
	Verdict               FinancialReviewVerdict `json:"verdict"`
	RecommendedBudget     *BudgetRecommendation  `json:"recommended_budget,omitempty"`
	EstimatedCost         *EstimatedCost         `json:"estimated_cost,omitempty"`
	Assumptions           []string               `json:"assumptions"`
	Risks                 []string               `json:"risks"`
	RequiredCorrections   []string               `json:"required_corrections"`
	MissingInformation    []string               `json:"missing_information"`
	Summary               string                 `json:"summary"`
}

// ComputeReviewCanonicalHash computes a deterministic SHA-256 hex digest of the canonical review payload.
func ComputeReviewCanonicalHash(payload ReviewCanonicalPayload) (string, error) {
	if payload.Assumptions == nil {
		payload.Assumptions = []string{}
	}
	if payload.Risks == nil {
		payload.Risks = []string{}
	}
	if payload.RequiredCorrections == nil {
		payload.RequiredCorrections = []string{}
	}
	if payload.MissingInformation == nil {
		payload.MissingInformation = []string{}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal canonical review payload: %w", err)
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
