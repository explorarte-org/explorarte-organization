package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// PromotionCanonicalPayload contains the resulting record fields of a campaign promotion
// used to compute its canonical SHA-256 seal.
type PromotionCanonicalPayload struct {
	OrganizationID                string               `json:"organization_id"`
	OwnerApprovalID               int64                `json:"owner_approval_id"`
	OwnerApprovalCanonicalHash    string               `json:"owner_approval_canonical_hash"`
	ProposalID                    int64                `json:"proposal_id"`
	ProposalCanonicalHash         string               `json:"proposal_canonical_hash"`
	FinancialReviewID             int64                `json:"financial_review_id"`
	FinancialReviewCanonicalHash  string               `json:"financial_review_canonical_hash"`
	ExecutionBudget               BudgetRecommendation `json:"execution_budget"`
	ExecutiveRootTaskID           int64                `json:"executive_root_task_id"`
	ExecutiveCorrelationID        string               `json:"executive_correlation_id"`
	ExecutiveSubmitIdempotencyKey string               `json:"executive_submit_idempotency_key"`
	Status                        PromotionStatus      `json:"status"`
	PromotedByRoleID              string               `json:"promoted_by_role_id"`
}

// ComputePromotionCanonicalHash computes a deterministic SHA-256 hex digest
// of the canonical promotion payload.
func ComputePromotionCanonicalHash(payload PromotionCanonicalPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal canonical promotion payload: %w", err)
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
