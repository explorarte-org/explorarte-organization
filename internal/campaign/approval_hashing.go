package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ApprovalCanonicalPayload contains the semantic content of an owner approval
// used to compute its canonical content hash.
type ApprovalCanonicalPayload struct {
	OrganizationID               string               `json:"organization_id"`
	ProposalID                   int64                `json:"proposal_id"`
	ProposalCanonicalHash        string               `json:"proposal_canonical_hash"`
	FinancialReviewID            int64                `json:"financial_review_id"`
	FinancialReviewCanonicalHash string               `json:"financial_review_canonical_hash"`
	ApprovedByRoleID             string               `json:"approved_by_role_id"`
	ExecutionBudget              BudgetRecommendation `json:"execution_budget"`
}

// ComputeApprovalCanonicalHash computes a deterministic SHA-256 hex digest
// of the canonical approval payload.
func ComputeApprovalCanonicalHash(payload ApprovalCanonicalPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal canonical approval payload: %w", err)
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
