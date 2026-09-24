package repositoryevidence

import (
	"context"
	"strings"
	"testing"
)

// An undelivered slot has two different causes that need opposite corrections: the set does
// not fit ONE snapshot (thin the demand), or the slot cannot be delivered even alone (drop
// or replace it). CoveragePlan.Unsupplyable is the measured line between them.

const unsupplyablePin = "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9"

func unsupplyableWorld() map[string]string {
	world := slotWorld()
	// The symbol the owner's campaign is ABOUT lives only in a test file.
	world["internal/digits/digits_test.go"] = "package digits\n\n// TESTONLY-MARKER commentary a designer would echo\nfunc TestExtractDigitRunsCoreCases(t *testing.T) {\n\t_ = \"abc123def45\"\n}\n"
	return world
}

func planFor(t *testing.T, world map[string]string, limits Limits, slots []EvidenceSlot) CoveragePlan {
	t.Helper()
	source := &literalSource{worlds: map[string]map[string]string{unsupplyablePin: world}}
	plan, err := PlanSlots(context.Background(), "explorarte", unsupplyablePin, source, limits, 24, slots)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

// A slot the eligible corpus cannot deliver is Unsupplyable, whatever the world's own
// search says about it: literalSource.Search happily matches the test file, and the real
// explorer refuses it.
func TestASlotWhoseOnlyDefinitionIsATestFileIsUnsupplyable(t *testing.T) {
	plan := planFor(t, unsupplyableWorld(), DefaultLimits(), []EvidenceSlot{
		{Subject: "TestExtractDigitRunsCoreCases", Relation: RelationDefinition},
		{Subject: "MaxDesignRounds", Relation: RelationDefinition},
	})
	if len(plan.Unsupplyable) != 1 || plan.Unsupplyable[0].Subject != "TestExtractDigitRunsCoreCases" {
		t.Fatalf("unsupplyable = %+v, want exactly TestExtractDigitRunsCoreCases/definition", plan.Unsupplyable)
	}
	if len(plan.Undelivered) != 1 {
		t.Fatalf("the supplyable slot was dragged into the refusal: %+v", plan.Undelivered)
	}
}

// Regression C: a set whose every slot is deliverable ALONE but which jointly exceeds one
// limit is capacity, never unsupplyable -- for each limit that can bind.
func TestAJointShortfallOfDeliverableSlotsIsNeverUnsupplyable(t *testing.T) {
	slots := []EvidenceSlot{
		{Subject: "MaxDesignRounds", Relation: RelationDefinition},
		{Subject: "DefaultLimits", Relation: RelationDefinition},
	}
	base := Limits{MaxFiles: 8, MaxRanges: 16, MaxBytes: 96 * 1024, MaxSearches: 8, MaxLines: 400}
	if plan := planFor(t, slotWorld(), base, slots); len(plan.Undelivered) != 0 {
		t.Fatalf("precondition: the pair fits the generous limits: %+v", plan)
	}
	for name, shrink := range map[string]func(Limits) Limits{
		"MaxSearches": func(l Limits) Limits { l.MaxSearches = 1; return l },
		"MaxRanges":   func(l Limits) Limits { l.MaxRanges = 2; return l },
		"MaxFiles":    func(l Limits) Limits { l.MaxFiles = 2; return l },
		"MaxBytes":    func(l Limits) Limits { l.MaxBytes = 170; return l },
	} {
		limits := shrink(base)
		plan := planFor(t, slotWorld(), limits, slots)
		if len(plan.Undelivered) == 0 {
			t.Errorf("%s: the shrunk limit never bound; the case proves nothing (%+v)", name, plan)
			continue
		}
		for _, slot := range plan.Undelivered {
			alone := planFor(t, slotWorld(), limits, []EvidenceSlot{slot})
			if len(alone.Undelivered) != 0 {
				t.Errorf("%s: %+v does not fit even alone; the fixture is not a joint shortfall", name, slot)
			}
		}
		if len(plan.Unsupplyable) != 0 {
			t.Errorf("%s: a joint capacity shortfall was called unsupplyable: %+v", name, plan.Unsupplyable)
		}
	}
}

// A lone slot the world cannot deliver is unsupplyable without a rerun.
func TestALoneUndeliverableSlotIsUnsupplyable(t *testing.T) {
	plan := planFor(t, slotWorld(), DefaultLimits(), []EvidenceSlot{{Subject: "GhostSymbol", Relation: RelationDefinition}})
	if len(plan.Unsupplyable) != 1 {
		t.Fatalf("unsupplyable = %+v", plan.Unsupplyable)
	}
}

// Regression D: the policy itself is untouched.
func TestTestFilesAreStillOutsideTheEligibleCorpus(t *testing.T) {
	for _, path := range []string{"internal/digits/digits_test.go", "a_test.go", "cmd/x/main_test.go"} {
		if EligibleEvidencePath(path) {
			t.Errorf("%s became eligible evidence", path)
		}
	}
	if !EligibleEvidencePath("internal/digits/digits.go") {
		t.Error("ordinary code stopped being eligible")
	}
}

// Regression E: no line of a test file enters the excerpts, whether or not the slot is
// classified as unsupplyable, and including on the solo reruns that classify it.
func TestNoTestFileLineEntersTheDeliveredContext(t *testing.T) {
	plan := planFor(t, unsupplyableWorld(), DefaultLimits(), []EvidenceSlot{
		{Subject: "TestExtractDigitRunsCoreCases", Relation: RelationDefinition},
		{Subject: "MaxDesignRounds", Relation: RelationDefinition},
	})
	for _, fragment := range plan.Fragments {
		if strings.HasSuffix(fragment.Path, "_test.go") || strings.Contains(fragment.Content, "TESTONLY-MARKER") {
			t.Fatalf("a test file line entered the model context: %+v", fragment)
		}
	}
	source := &literalSource{worlds: map[string]map[string]string{unsupplyablePin: unsupplyableWorld()}}
	explorer, err := NewExplorer("explorarte", unsupplyablePin, source, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fragments, _, err := GatherWithCoverage(context.Background(), explorer, Selection{
		Terms: []string{"TestExtractDigitRunsCoreCases"}, RequiredTerms: []string{"TestExtractDigitRunsCoreCases"},
		Slots:  []EvidenceSlot{{Subject: "TestExtractDigitRunsCoreCases", Relation: RelationDefinition}},
		Window: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if strings.HasSuffix(fragment.Path, "_test.go") || strings.Contains(fragment.Content, "TESTONLY-MARKER") {
			t.Fatalf("a test file line entered the model context: %+v", fragment)
		}
	}
}

// A sensor outage during the solo rerun is an error, not a verdict that a slot is
// unsupplyable.
func TestSoloRerunOutagePropagates(t *testing.T) {
	source := &literalSource{
		worlds:     map[string]map[string]string{unsupplyablePin: slotWorld()},
		failSearch: context.DeadlineExceeded,
	}
	if _, err := PlanSlots(context.Background(), "explorarte", unsupplyablePin, source, DefaultLimits(), 24, []EvidenceSlot{
		{Subject: "GhostSymbol", Relation: RelationDefinition},
		{Subject: "MaxDesignRounds", Relation: RelationDefinition},
	}); err == nil {
		t.Fatal("an outage was reported as a plan")
	}
}
