package repositoryevidence

import (
	"context"
	"strings"
	"testing"
)

// Checkpoint D's delivery half, pinned at the selection layer: slots are
// satisfied MANDATORILY, across file boundaries, under one shared budget --
// before any incidental exploration spends a byte.

func slotWorld() map[string]string {
	return map[string]string{
		// Declaration of the first subject.
		"internal/executive/types.go": "package executive\n\ntype Limits struct {\n\tMaxDesignRounds int\n}\n",
		// Application of the first subject, in a DIFFERENT file -- exactly
		// the layout that starved R15's round 2.
		"internal/executive/orchestrator.go": "package executive\n\nfunc step(l Limits) bool {\n\treturn l.MaxDesignRounds > 0\n}\n",
		// A competing subject, to force the old starvation shape.
		"internal/executive/budget.go": "package executive\n\nfunc DefaultLimits() Limits {\n\treturn Limits{}\n}\n",
	}
}

func slotSet() []EvidenceSlot {
	return []EvidenceSlot{
		{Subject: "MaxDesignRounds", Relation: RelationDefinition},
		{Subject: "MaxDesignRounds", Relation: RelationApplication},
		{Subject: "DefaultLimits", Relation: RelationDefinition},
	}
}

func TestSlotsAreSatisfiedAcrossFilesUnderOneBudget(t *testing.T) {
	source := &literalSource{worlds: map[string]map[string]string{"14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9": slotWorld()}}
	explorer, err := NewExplorer("explorarte", "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9", source, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	selection := Selection{
		Terms:         []string{"MaxDesignRounds", "DefaultLimits"},
		RequiredTerms: []string{"MaxDesignRounds", "DefaultLimits"},
		Slots:         slotSet(),
		Window:        24,
	}
	fragments, uncovered, err := GatherWithCoverage(context.Background(), explorer, selection)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(uncovered) != 0 {
		t.Fatalf("slots left uncovered: %+v", uncovered)
	}
	joined := ""
	for _, fragment := range fragments {
		joined += fragment.Path + "\n"
	}
	if !strings.Contains(joined, "types.go") || !strings.Contains(joined, "orchestrator.go") {
		t.Fatalf("the definition file or the application file never made it into the diet:\n%s", joined)
	}
}

// GUARD: the same slot set in two PERMUTATIONS must produce an identical
// verdict, identical coverage, and identical spend. Admission and delivery
// canonicalize once, so arrival order is never a variable.
func TestSlotOrderCannotChangeTheVerdictOrTheSpend(t *testing.T) {
	run := func(order func([]EvidenceSlot) []EvidenceSlot) ([]Fragment, []EvidenceSlot, Explorer, error) {
		source := &literalSource{worlds: map[string]map[string]string{"14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9": slotWorld()}}
		explorer, err := NewExplorer("explorarte", "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9", source, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		slots := order(slotSet())
		selection := Selection{
			Terms:         []string{"MaxDesignRounds", "DefaultLimits"},
			RequiredTerms: []string{"MaxDesignRounds", "DefaultLimits"},
			Slots:         slots,
			Window:        24,
		}
		fragments, uncovered, err := GatherWithCoverage(context.Background(), explorer, selection)
		return fragments, uncovered, *explorer, err
	}
	reverse := func(slots []EvidenceSlot) []EvidenceSlot {
		out := make([]EvidenceSlot, len(slots))
		copy(out, slots)
		for first, last := 0, len(out)-1; first < last; first, last = first+1, last-1 {
			out[first], out[last] = out[last], out[first]
		}
		return out
	}

	firstFragments, firstUncovered, firstExplorer, firstErr := run(func(s []EvidenceSlot) []EvidenceSlot { return s })
	secondFragments, secondUncovered, secondExplorer, secondErr := run(reverse)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("both runs must succeed: %v / %v", firstErr, secondErr)
	}
	if len(firstUncovered) != 0 || len(secondUncovered) != 0 {
		t.Fatalf("permutations disagree on coverage: %+v / %+v", firstUncovered, secondUncovered)
	}
	refs := func(fragments []Fragment) map[string]bool {
		set := map[string]bool{}
		for _, fragment := range fragments {
			set[fragment.Reference()] = true
		}
		return set
	}
	firstRefs, secondRefs := refs(firstFragments), refs(secondFragments)
	for ref := range firstRefs {
		if !secondRefs[ref] {
			t.Fatalf("permutations disagree on delivered fragments: %q only in the first", ref)
		}
	}
	for ref := range secondRefs {
		if !firstRefs[ref] {
			t.Fatalf("permutations disagree on delivered fragments: %q only in the second", ref)
		}
	}
	firstSearches, firstFiles, firstRanges, firstBytes := firstExplorer.Spent()
	secondSearches, secondFiles, secondRanges, secondBytes := secondExplorer.Spent()
	if [4]int{firstSearches, firstFiles, firstRanges, firstBytes} != [4]int{secondSearches, secondFiles, secondRanges, secondBytes} {
		t.Fatalf("permutations spent differently: %+v vs %+v",
			[4]int{firstSearches, firstFiles, firstRanges, firstBytes},
			[4]int{secondSearches, secondFiles, secondRanges, secondBytes})
	}
}

// GUARD: X/definition + X/application across two unique excerpts must cost
// EXACTLY two ranges. The cursor makes each candidate be read at most once
// per subject, and one read satisfies every relation of that subject its
// content proves -- the double-charge that used to turn a fitting pair into
// three reads against a two-range budget is gone.
func TestTwoSlotsOfOneSubjectCostExactlyTheirUniqueExcerpts(t *testing.T) {
	world := map[string]string{
		"internal/x/a_decl.go": "package x\n\nvar X = 1\n",
		"internal/x/b_use.go":  "package x\n\nfunc use() bool { return X > 0 }\n",
	}
	source := &literalSource{worlds: map[string]map[string]string{"14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9": world}}
	explorer, err := NewExplorer("explorarte", "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9", source, Limits{MaxFiles: 8, MaxRanges: 2, MaxBytes: 96 * 1024, MaxSearches: 12, MaxLines: 400})
	if err != nil {
		t.Fatal(err)
	}
	selection := Selection{
		Terms: []string{"X"},
		Slots: []EvidenceSlot{
			{Subject: "X", Relation: RelationDefinition},
			{Subject: "X", Relation: RelationApplication},
		},
		Window: 4,
	}
	_, uncovered, err := GatherWithCoverage(context.Background(), explorer, selection)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(uncovered) != 0 {
		t.Fatalf("a fitting pair reported uncovered under its own two ranges: %+v", uncovered)
	}
	searches, files, ranges, _ := explorer.Spent()
	if ranges != 2 {
		t.Fatalf("ranges spent = %d, want exactly 2", ranges)
	}
	if searches != 1 {
		t.Fatalf("searches spent = %d, want exactly 1 (one subject, cached)", searches)
	}
	if files != 2 {
		t.Fatalf("files touched = %d, want exactly the two unique excerpts' files", files)
	}
}

// The joint-admission verdict on a set the world cannot deliver together:
// every undelivered slot is named, nothing else is invented.
func TestPlanSlotsNamesUndeliveredSlots(t *testing.T) {
	source := &literalSource{worlds: map[string]map[string]string{"14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9": slotWorld()}}
	slots := append(slotSet(), EvidenceSlot{Subject: "GhostSymbol", Relation: RelationDefinition})
	plan, err := PlanSlots(context.Background(), "explorarte", "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9", source, DefaultLimits(), 24, slots)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Undelivered) != 1 || plan.Undelivered[0].Subject != "GhostSymbol" {
		t.Fatalf("undelivered = %+v, want exactly GhostSymbol/definition", plan.Undelivered)
	}
	if len(plan.Covered) == 0 {
		t.Fatal("the deliverable slots vanished from the plan")
	}
}

// A sensor outage aborts admission instead of masquerading as an empty world:
// the caller sees the error and classifies it as infrastructure.
func TestPlanSlotsPropagatesSensorOutages(t *testing.T) {
	source := &literalSource{
		worlds:     map[string]map[string]string{"14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9": slotWorld()},
		failSearch: context.DeadlineExceeded,
	}
	if _, err := PlanSlots(context.Background(), "explorarte", "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9", source, DefaultLimits(), 24, slotSet()); err == nil {
		t.Fatal("a broken observer was accepted as an admission verdict")
	}
}

// Capacity itself is a verdict, not an outage: when the shared budget cannot
// fit the whole set, admission reports the shortfall as undelivered slots --
// never as an infrastructure failure.
func TestPlanSlotsReportsCapacityShortfallsAsUndelivered(t *testing.T) {
	source := &literalSource{worlds: map[string]map[string]string{"14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9": slotWorld()}}
	tight := Limits{MaxFiles: 8, MaxRanges: 16, MaxBytes: 96 * 1024, MaxSearches: 1, MaxLines: 400}
	plan, err := PlanSlots(context.Background(), "explorarte", "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9", source, tight, 24, []EvidenceSlot{
		{Subject: "MaxDesignRounds", Relation: RelationDefinition},
		{Subject: "DefaultLimits", Relation: RelationDefinition},
		{Subject: "UnrelatedThing", Relation: RelationDefinition},
	})
	if err != nil {
		t.Fatalf("capacity is a verdict, not an error: %v", err)
	}
	found := false
	for _, slot := range plan.Undelivered {
		if slot.Subject == "UnrelatedThing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the slot beyond the shared search capacity was not reported: %+v", plan.Undelivered)
	}
}
