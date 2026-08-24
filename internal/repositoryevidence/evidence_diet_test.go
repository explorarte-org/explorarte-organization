package repositoryevidence

import (
	"context"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-017-R11 lost a campaign because a worker-facing excerpt of a
// TEST file -- its long explanatory comment -- was echoed into a candidate
// design, and the egress gate refused the bundle. The diet fix removes the
// test corpus from citable evidence at every entry point. These guards pin
// each entry point separately, because they fail differently: discovery can
// be filtered, direct reads must refuse, and the git backend must pre-filter
// BEFORE its candidate limit so real evidence cannot be evicted by files the
// host would refuse anyway.

// Entry point: discovery. A subject mentioned by both a production file and a
// test file yields only the production candidate.
func TestSearchDropsTestFileMatches(t *testing.T) {
	source := &literalSource{worlds: map[string]map[string]string{probeSHA: {
		"internal/executive/budget.go":              "package executive\n\nfunc apply() { _ = MaxDesignRounds }\n",
		"internal/executive/budget_fixture_test.go": "package executive\n\nvar _ = MaxDesignRounds\n",
	}}}
	explorer, err := NewExplorer("explorarte-organization", probeSHA, source, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	matches, err := explorer.Search(context.Background(), "MaxDesignRounds")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 || matches[0].Path != "internal/executive/budget.go" {
		t.Fatalf("a test file survived discovery: %+v", matches)
	}
}

// Entry point: direct read. The authoritative boundary -- no explicitly named
// test path can become a Fragment through Selection.Paths or any other route.
func TestReadRefusesTestFilesEvenWhenNamedDirectly(t *testing.T) {
	explorer, err := NewExplorer("explorarte-organization", probeSHA,
		&literalSource{worlds: map[string]map[string]string{probeSHA: {
			"internal/executive/x_test.go": "package executive\n\nvar _ = MaxDesignRounds\n",
		}}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explorer.Read(context.Background(), "internal/executive/x_test.go", 1, 10); err == nil {
		t.Fatal("a test file was read into citable evidence")
	} else if !strings.Contains(err.Error(), "not eligible evidence") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

// And the same boundary holds through the selection pipeline: a goal that
// names a test path outright gathers nothing from it.
func TestNamedTestPathYieldsNoFragments(t *testing.T) {
	explorer, err := NewExplorer("explorarte-organization", probeSHA,
		&literalSource{worlds: map[string]map[string]string{probeSHA: {
			"internal/executive/x_test.go": "package executive\n\nvar _ = MaxDesignRounds\n",
		}}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fragments, err := Gather(context.Background(), explorer, Selection{
		Paths:  []string{"internal/executive/x_test.go"},
		Terms:  []string{"MaxDesignRounds"},
		Window: 4,
	})
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(fragments) != 0 {
		t.Fatalf("a named test path produced %d fragments of citable evidence", len(fragments))
	}
}

// The probe shares the diet, so A1 stays symmetric with what the evidence
// mechanism can actually supply.
//
// An obligation whose only APPLICATION lives in a test file is mechanically
// unsupplyable once tests leave the corpus: the honest answer is false with
// NO error, which routes the round to fail-closed preflight rather than to a
// sensor outage.
func TestProbeTreatsTestOnlyApplicationAsUnsupplyable(t *testing.T) {
	supplied, err := ProbeSubjectSupply(context.Background(), "explorarte-organization", probeSHA,
		&literalSource{worlds: map[string]map[string]string{probeSHA: {
			"internal/executive/types.go":            "package executive\n\nMaxDepartmentReplans int\n",
			"internal/executive/replans_use_test.go": "package executive\n\nif n > MaxDepartmentReplans {\n",
		}}}, DefaultLimits(), "MaxDepartmentReplans",
		[]string{"definition", "application"}, 24)
	if err != nil {
		t.Fatalf("the missing test-only slot surfaced as a sensor failure: %v", err)
	}
	if !supplied["definition"] {
		t.Errorf("the production declaration stopped being supplyable: %v", supplied)
	}
	if supplied["application"] {
		t.Errorf("an application that exists only in a test file was declared supplyable")
	}
}

// When production ALSO applies the symbol, the productive site is what makes
// the slot supplyable -- the test mention neither helps nor blocks it.
func TestProbeSuppliesProductionApplicationDespiteTestMentions(t *testing.T) {
	supplied, err := ProbeSubjectSupply(context.Background(), "explorarte-organization", probeSHA,
		&literalSource{worlds: map[string]map[string]string{probeSHA: {
			"internal/executive/types.go":            "package executive\n\nMaxDepartmentReplans int\n",
			"internal/executive/replans_use_test.go": "package executive\n\nif n > MaxDepartmentReplans {\n",
			"internal/executive/orchestrator.go":     "package executive\n\nfunc replans(l Limits) bool { return l.MaxDepartmentReplans > 0 }\n",
		}}}, DefaultLimits(), "MaxDepartmentReplans",
		[]string{"definition", "application"}, 24)
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if !supplied["definition"] || !supplied["application"] {
		t.Fatalf("production slots went missing while tests were excluded: %v", supplied)
	}
}
