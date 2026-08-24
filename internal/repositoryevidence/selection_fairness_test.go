package repositoryevidence

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-017-R10's selection starvation, guarded. Required subjects
// are searched first but one subject's matches were read to exhaustion before
// the next required subject was ever touched: MaxDepartmentReplans/application
// had been supplied in round 1 and vanished in round 2, because another
// subject's abundance spent the range budget first.
//
// The fix is a fair reservation: every required subject takes its ranked head
// in a coverage pass BEFORE anything optional reads. These tests hold that
// property against a flooded world under tight budgets.

func floodWorld() *literalSource {
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("internal/executive/alpha_extra_%02d.go", i)] =
			fmt.Sprintf("package executive\n\n// filler %d\nvar alphaUse%02d = AlphaChannel\n", i, i)
	}
	files["internal/executive/beta.go"] = `package executive

type BetaLimit struct{}

func useBeta(b BetaLimit) bool { return b != BetaLimit{} }
`
	files["internal/executive/replans.go"] = `package executive

const capValue = MaxDepartmentReplans
`
	return &literalSource{worlds: map[string]map[string]string{probeSHA: files}}
}

func gatherPaths(t *testing.T, limits Limits, selection Selection) map[string]int {
	t.Helper()
	explorer, err := NewExplorer("explorarte-organization", probeSHA, floodWorld(), limits)
	if err != nil {
		t.Fatal(err)
	}
	fragments, err := Gather(context.Background(), explorer, selection)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	counts := map[string]int{}
	for _, fragment := range fragments {
		counts[fragment.Path]++
	}
	return counts
}

// With physical capacity for everyone, no required subject may lose its best
// candidates to another subject's extras -- whichever subject floods first.
func TestRequiredSubjectsGetTheirHeadCandidatesBeforeExtras(t *testing.T) {
	text := "Ground AlphaChannel, BetaLimit and MaxDepartmentReplans with citations."
	selection := SelectionForRequirements(text,
		[]string{"AlphaChannel", "BetaLimit", "MaxDepartmentReplans"}, 4)
	if len(selection.RequiredTerms) != 3 {
		t.Fatalf("required terms=%v, want the three obligations", selection.RequiredTerms)
	}

	tight := Limits{MaxFiles: 5, MaxRanges: 5, MaxBytes: 96 * 1024, MaxSearches: 24, MaxLines: 400}
	counts := gatherPaths(t, tight, selection)

	if counts["internal/executive/beta.go"] == 0 {
		t.Fatalf("BetaLimit was starved by AlphaChannel's extras: %v", counts)
	}
	// THE R10 ABLATION: an application supplied in an earlier round must not
	// be expelled by other subjects' abundance in the next.
	if counts["internal/executive/replans.go"] == 0 {
		t.Fatalf("MaxDepartmentReplans/application was expelled again: %v", counts)
	}
	alphaFiles := 0
	for path := range counts {
		if strings.Contains(path, "alpha_extra") {
			alphaFiles++
		}
	}
	if alphaFiles > requiredHeadSize {
		t.Fatalf("extras read %d alpha files during the coverage pass: %v", alphaFiles, counts)
	}
}

// The reservation is not rationing: when capacity allows, the extras come back
// after every obligation has had its turn.
func TestExtrasReturnWhenCapacityAllows(t *testing.T) {
	text := "Ground AlphaChannel, BetaLimit and MaxDepartmentReplans with citations."
	selection := SelectionForRequirements(text,
		[]string{"AlphaChannel", "BetaLimit", "MaxDepartmentReplans"}, 4)

	generous := Limits{MaxFiles: 30, MaxRanges: 40, MaxBytes: 96 * 1024, MaxSearches: 24, MaxLines: 400}
	counts := gatherPaths(t, generous, selection)

	if len(counts) < 12 {
		t.Fatalf("a generous reading gathered %d files, want the extras too: %v", len(counts), counts)
	}
	if counts["internal/executive/beta.go"] == 0 || counts["internal/executive/replans.go"] == 0 {
		t.Fatalf("generous budget lost a required subject: %v", counts)
	}
}
