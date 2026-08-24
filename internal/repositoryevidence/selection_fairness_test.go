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

// A starving budget must interleave, not queue: with room for exactly one
// candidate each, every required subject keeps its FIRST candidate and nobody
// keeps a second. Sequential head-reading would have handed the first two
// slots to the earliest subject's pair and left the last subject empty.
func TestCoverageIsRoundRobinUnderStarvation(t *testing.T) {
	files := map[string]string{}
	for _, spec := range []struct{ subject, prefix string }{
		{"AlphaChannel", "alpha"},
		{"BetaLimit", "beta"},
		{"GammaThing", "gamma"},
	} {
		files["internal/executive/"+spec.prefix+"_first.go"] =
			"package executive\n\n" + spec.subject + " declared here\n"
		files["internal/executive/"+spec.prefix+"_second.go"] =
			"package executive\n\nvar extra = " + spec.subject + "\n"
	}
	source := &literalSource{worlds: map[string]map[string]string{probeSHA: files}}

	explorer, err := NewExplorer("explorarte-organization", probeSHA, source,
		Limits{MaxFiles: 3, MaxRanges: 3, MaxBytes: 96 * 1024, MaxSearches: 24, MaxLines: 400})
	if err != nil {
		t.Fatal(err)
	}
	selection := SelectionForRequirements("Ground all three.",
		[]string{"AlphaChannel", "BetaLimit", "GammaThing"}, 4)
	fragments, err := Gather(context.Background(), explorer, selection)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	seen := map[string]bool{}
	for _, fragment := range fragments {
		seen[fragment.Path] = true
	}
	for _, prefix := range []string{"alpha", "beta", "gamma"} {
		if !seen["internal/executive/"+prefix+"_first.go"] {
			t.Fatalf("%s lost its first candidate under starvation: %v", prefix, seen)
		}
		if seen["internal/executive/"+prefix+"_second.go"] {
			t.Fatalf("%s's second candidate was read before another subject had its first", prefix)
		}
	}
}

// A path named outright is orientation, not an obligation: it yields to
// required coverage when the budget cannot hold both.
func TestNamedPathsYieldToRequiredCoverage(t *testing.T) {
	world := map[string]string{
		"docs/readme.md":                     "about this repository",
		"internal/executive/alpha_first.go":  "package executive\n\nAlphaChannel declared here\n",
		"internal/executive/alpha_second.go": "package executive\n\nvar extra = AlphaChannel\n",
		"internal/executive/beta_first.go":   "package executive\n\nBetaLimit declared here\n",
		"internal/executive/beta_second.go":  "package executive\n\nvar extra2 = BetaLimit\n",
	}
	source := &literalSource{worlds: map[string]map[string]string{probeSHA: world}}

	explorer, err := NewExplorer("explorarte-organization", probeSHA, source,
		Limits{MaxFiles: 2, MaxRanges: 2, MaxBytes: 96 * 1024, MaxSearches: 24, MaxLines: 400})
	if err != nil {
		t.Fatal(err)
	}
	selection := Selection{
		Terms:         []string{"AlphaChannel", "BetaLimit"},
		RequiredTerms: []string{"AlphaChannel", "BetaLimit"},
		Paths:         []string{"internal/executive", "docs/readme.md"},
		Window:        4,
	}
	fragments, err := Gather(context.Background(), explorer, selection)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	seen := map[string]bool{}
	for _, fragment := range fragments {
		seen[fragment.Path] = true
	}
	if !seen["internal/executive/alpha_first.go"] || !seen["internal/executive/beta_first.go"] {
		t.Fatalf("required coverage yielded to a named path: %v", seen)
	}
	if seen["docs/readme.md"] {
		t.Fatalf("an optional path was read while obligations went unsupplied: %v", seen)
	}
}
