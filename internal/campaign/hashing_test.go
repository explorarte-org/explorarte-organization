package campaign

import (
	"regexp"
	"testing"
)

func TestComputeCanonicalHash(t *testing.T) {
	p1 := CanonicalPayload{
		Title:              "Q4 Marketing Expansion",
		Goal:               "Acquire 500 new active creators",
		AcceptanceCriteria: []string{"CPA < $10", "Conversion rate > 3%"},
		Requirements: []ProposalRequirement{
			{Key: "landing_page", Description: "New responsive landing page", Required: true},
		},
		Budget: &ProposalBudget{
			Currency:  "USD",
			MaxAmount: 5000.0,
			Source:    BudgetSourceOwnerLimit,
		},
		Assumptions:   []string{"Creator onboarding is live"},
		Risks:         []string{"Ad fatigue"},
		OpenQuestions: []string{"Which channels to prioritize?"},
	}

	h1, err := ComputeCanonicalHash(p1)
	if err != nil {
		t.Fatalf("ComputeCanonicalHash failed: %v", err)
	}

	hexRegex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !hexRegex.MatchString(h1) {
		t.Fatalf("expected 64-char hex hash, got %q", h1)
	}

	// Determinism
	h2, err := ComputeCanonicalHash(p1)
	if err != nil {
		t.Fatalf("second hash failed: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected identical hash %q, got %q", h1, h2)
	}

	// Different payload -> different hash
	p2 := p1
	p2.Title = "Q4 Marketing Expansion - Revised"
	h3, err := ComputeCanonicalHash(p2)
	if err != nil {
		t.Fatalf("modified hash failed: %v", err)
	}
	if h1 == h3 {
		t.Fatalf("expected different hash for modified payload, but both were %q", h1)
	}
}
