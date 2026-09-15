package campaign_test

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

func TestComputeReviewCanonicalHash(t *testing.T) {
	payload1 := campaign.ReviewCanonicalPayload{
		ProposalID:            1,
		ProposalCanonicalHash: "hash123",
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictRecommended,
		Summary:               "Financially viable under current ceilings.",
		Assumptions:           []string{"Assump A"},
		Risks:                 []string{"Risk A"},
	}

	hash1, err := campaign.ComputeReviewCanonicalHash(payload1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hash1) != 64 {
		t.Fatalf("expected 64 char hex hash, got %d chars: %s", len(hash1), hash1)
	}

	// Deterministic
	hash2, _ := campaign.ComputeReviewCanonicalHash(payload1)
	if hash1 != hash2 {
		t.Fatalf("expected deterministic hash, got %s and %s", hash1, hash2)
	}

	// Different payload -> different hash
	payload2 := payload1
	payload2.Verdict = campaign.VerdictChangesRequested
	hash3, _ := campaign.ComputeReviewCanonicalHash(payload2)
	if hash1 == hash3 {
		t.Fatalf("expected different hash for changed verdict")
	}
}
