package repositoryevidence

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// Local smoke #34 (root 1773): the designer cited mission_phase.go lines 131-179; the adjudicator's
// own searches were issued lines 1-48 of the same file, so it could not see what the design stood
// on. A judge's build now reads the ranges the design cites, at this commit, before any search.
func TestAJudgeIsShownTheRangesTheDesignCites(t *testing.T) {
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = fmt.Sprintf("// line %d of mission_phase.go", i+1)
	}
	source := newSource()
	source.lines["internal/executive/mission_phase.go"] = len(lines)
	source.content["internal/executive/mission_phase.go"] = strings.Join(lines, "\n")
	provider, err := NewProvider("explorarte-organization", source, DefaultLimits(), 4)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "repository://explorarte-organization@" + shaA + "/"
	records, err := provider.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{
		RepositoryBaseSHA: shaA,
		RepositoryQuery:   "judge the design of internal/executive/orchestrator.go and its driveDepartments handling",
		RepositoryCitations: []string{
			prefix + "internal/executive/mission_phase.go#L131-L179",
			prefix + "internal/executive/mission_phase.go#L325-L373",
			// Another commit describes another world: dropped.
			"repository://explorarte-organization@" + shaB + "/internal/executive/mission_phase.go#L1-L10",
			// Not a range: dropped.
			prefix + "internal/executive/mission_phase.go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var references []string
	for _, record := range records {
		references = append(references, record.Reference)
	}
	joined := strings.Join(references, "\n")
	for _, want := range []string{"mission_phase.go#L131-L179", "mission_phase.go#L325-L373"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the judge was not shown the cited %s:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "mission_phase.go#L1-L10") {
		t.Errorf("a citation of another commit was read:\n%s", joined)
	}
	if !strings.Contains(joined, "orchestrator.go") {
		t.Errorf("the judge's own exploration was crowded out:\n%s", joined)
	}
}

// Cited ranges take at most half the range budget, so the judge keeps room to look for itself.
func TestCitedRangesTakeAtMostHalfTheRangeBudget(t *testing.T) {
	lines := make([]string, 2000)
	for i := range lines {
		lines[i] = fmt.Sprintf("// %d", i+1)
	}
	source := newSource()
	source.lines["internal/executive/big.go"] = len(lines)
	source.content["internal/executive/big.go"] = strings.Join(lines, "\n")
	limits := DefaultLimits()
	cited := make([]CitedRange, 0, limits.MaxRanges)
	for i := 0; i < limits.MaxRanges; i++ {
		cited = append(cited, CitedRange{Path: "internal/executive/big.go", Start: i*10 + 1, End: i*10 + 5})
	}
	explorer, err := NewExplorer("explorarte-organization", shaA, source, limits)
	if err != nil {
		t.Fatal(err)
	}
	fragments, err := Gather(context.Background(), explorer, Selection{Cited: cited, Window: 4})
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, fragment := range fragments {
		if fragment.Path == "internal/executive/big.go" {
			read++
		}
	}
	if read != limits.MaxRanges/maxCitedShare {
		t.Fatalf("%d cited ranges were read, want %d (half of %d)", read, limits.MaxRanges/maxCitedShare, limits.MaxRanges)
	}
}
