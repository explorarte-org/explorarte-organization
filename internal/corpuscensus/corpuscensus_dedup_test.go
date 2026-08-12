package corpuscensus

import "testing"

func strp(s string) *string { return &s }
func intp(n int) *int       { return &n }

func TestResolveWorkIdentityPrefersDOIOverArxiv(t *testing.T) {
	p := BronzePaper{CanonicalID: "x", DOI: strp("10.1/x"), ArxivID: strp("2401.0001")}
	kind, key := ResolveWorkIdentity(p)
	if kind != IdentityDOI || key != "doi:10.1/x" {
		t.Fatalf("kind=%s key=%s", kind, key)
	}
}

func TestResolveWorkIdentityFallsBackToTitleYear(t *testing.T) {
	p := BronzePaper{CanonicalID: "x", Title: "Some Paper: A Study!", Year: intp(2025)}
	kind, key := ResolveWorkIdentity(p)
	if kind != IdentityTitleYear {
		t.Fatalf("kind=%s", kind)
	}
	if key != "title_year:some paper a study|2025" {
		t.Fatalf("key=%q", key)
	}
}

func TestGroupWorksMergesRowsSharingArxivID(t *testing.T) {
	papers := []BronzePaper{
		{CanonicalID: "doi:10.1/a", DOI: strp("10.1/a"), ArxivID: strp("2401.0001"), Title: "A"},
		{CanonicalID: "arxiv:2401.0001", ArxivID: strp("2401.0001"), Title: "A (preprint)"},
		{CanonicalID: "doi:10.1/b", DOI: strp("10.1/b"), Title: "B"},
	}
	groups := GroupWorks(papers)
	if len(groups) != 2 {
		t.Fatalf("expected 2 works, got %d: %+v", len(groups), groups)
	}
	var sawMergedGroup bool
	for _, g := range groups {
		if len(g) == 2 {
			sawMergedGroup = true
		}
	}
	if !sawMergedGroup {
		t.Fatalf("expected one group of size 2 (doi:10.1/a + arxiv:2401.0001), got %+v", groups)
	}
}

func TestGroupWorksKeepsDistinctTitleYearApart(t *testing.T) {
	papers := []BronzePaper{
		{CanonicalID: "t1", Title: "Alpha", Year: intp(2024)},
		{CanonicalID: "t2", Title: "Beta", Year: intp(2024)},
	}
	groups := GroupWorks(papers)
	if len(groups) != 2 {
		t.Fatalf("expected 2 works, got %d", len(groups))
	}
}

func TestGroupWorksMergesSameNormalizedTitleAndYear(t *testing.T) {
	papers := []BronzePaper{
		{CanonicalID: "t1", Title: "Some Paper!", Year: intp(2024)},
		{CanonicalID: "t2", Title: "some   paper", Year: intp(2024)},
	}
	groups := GroupWorks(papers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 work (same normalized title+year), got %d: %+v", len(groups), groups)
	}
}

func TestSelectCanonicalArtifactPrefersConfirmedVenue(t *testing.T) {
	group := []BronzePaper{
		{CanonicalID: "arxiv:v1", ArxivID: strp("2401.0001"), Year: intp(2024)},
		{CanonicalID: "doi:pub", DOI: strp("10.1/x"), Venue: strp("NeurIPS 2025"), Year: intp(2025)},
	}
	canonical, reason := SelectCanonicalArtifact(group)
	if canonical.CanonicalID != "doi:pub" {
		t.Fatalf("canonical=%s reason=%s", canonical.CanonicalID, reason)
	}
}

func TestSelectCanonicalArtifactPrefersHigherYearWhenNeitherHasVenue(t *testing.T) {
	group := []BronzePaper{
		{CanonicalID: "arxiv:v1", ArxivID: strp("2401.0001"), Year: intp(2024)},
		{CanonicalID: "arxiv:v2", ArxivID: strp("2401.0001v2"), Year: intp(2025)},
	}
	canonical, _ := SelectCanonicalArtifact(group)
	if canonical.CanonicalID != "arxiv:v2" {
		t.Fatalf("canonical=%s, expected the higher-year artifact", canonical.CanonicalID)
	}
}

func TestSelectCanonicalArtifactIsDeterministicOnTies(t *testing.T) {
	group := []BronzePaper{
		{CanonicalID: "z", Year: intp(2024)},
		{CanonicalID: "a", Year: intp(2024)},
	}
	canonical, _ := SelectCanonicalArtifact(group)
	if canonical.CanonicalID != "a" {
		t.Fatalf("canonical=%s, expected lexically smaller canonical_id on a true tie", canonical.CanonicalID)
	}
}
