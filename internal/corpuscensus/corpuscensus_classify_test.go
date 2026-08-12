package corpuscensus

import "testing"

func TestClassifySourceTypeFlagsKnownEvaluationDatasets(t *testing.T) {
	if got := ClassifySourceType("HotpotQA: A Dataset for Diverse, Explainable Multi-hop QA", ""); got != "evaluation" {
		t.Fatalf("got %q", got)
	}
	if got := ClassifySourceType("Retrieval-Augmented Generation for Knowledge-Intensive Tasks", ""); got != "paper" {
		t.Fatalf("got %q, expected paper", got)
	}
}

func TestClassifyAuthorityTierRequiresConfirmedVenueForTierA(t *testing.T) {
	if got := ClassifyAuthorityTier(BronzePaper{Venue: strp("ACL 2025")}); got != TierA {
		t.Fatalf("got %s", got)
	}
	if got := ClassifyAuthorityTier(BronzePaper{}); got != TierB {
		t.Fatalf("got %s, expected TierB with no venue", got)
	}
}

func TestLooksLikeReferencesPageMatchesExplicitHeading(t *testing.T) {
	if !LooksLikeReferencesPage(10, 12, "References\n\nSmith, J. (2024)...") {
		t.Fatal("expected true for explicit References heading")
	}
}

func TestLooksLikeReferencesPageIgnoresEarlyHighDensityPage(t *testing.T) {
	// Even a citation-dense page should not be flagged if it's in the
	// first 70% of the document (e.g. a related-work section, not the
	// bibliography) -- only the heading match should catch that case.
	dense := "See (2021) and (2022) and (2023) and (2024) and et al. et al. et al."
	if LooksLikeReferencesPage(2, 12, dense) {
		t.Fatal("expected false for an early-document dense page without a heading")
	}
}

func TestLooksLikeReferencesPageMatchesTrailingHighDensityPage(t *testing.T) {
	dense := "Smith et al. (2021) arxiv: 2101.00001 doi: 10.1/x Jones et al. (2022) arxiv: 2201.00002"
	if !LooksLikeReferencesPage(11, 12, dense) {
		t.Fatal("expected true for a trailing, citation-dense page")
	}
}

func TestLooksLikeReferencesPageFalseForNormalTrailingPage(t *testing.T) {
	if LooksLikeReferencesPage(12, 12, "In conclusion, our method improves accuracy by 4 points.") {
		t.Fatal("expected false for a normal low-citation-density trailing page")
	}
}
