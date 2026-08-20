package composition

import (
	"strings"
	"testing"
)

func healthy() Observation {
	return Observation{
		KeyDatabaseSchemaTip:          "55",
		KeyRuntimeSchemaCompatibility: "55,56",
		KeyOrganizationRevision:       "19",
		KeyEgressBoundRevision:        "19",
		KeyRuntimeDesiredSHA:          "abc123",
		KeyRuntimeObservedSHA:         "abc123",
	}
}

func TestAWellFormedGraphStillRefusesActivationWhenTheValuesDisagree(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	// The graph does not change. Only the world does: egress is bound to
	// the previous revision. This is the exact seam canonicalsync guards,
	// and it is invisible to topology -- every required key is present and
	// provided.
	obs := healthy()
	obs[KeyEgressBoundRevision] = "18"

	admitted, refused := g.Admissible(obs)
	for _, id := range []string{"runtime-orgd", "assignment-controller"} {
		reason, ok := refused[id]
		if !ok {
			t.Fatalf("%s must not be admitted while egress lags the registry; admitted=%v", id, admitted)
		}
		for _, want := range []string{"canonical.egress.binding=18", "canonical.registry.revision=19"} {
			if !strings.Contains(reason, want) {
				t.Errorf("the refusal must carry the actual values; %q missing from %q", want, reason)
			}
		}
	}
	// The components that establish those facts are unaffected: refusing
	// the consumers is what lets the producers go fix it.
	for _, id := range []string{"database-schema", "canonical-registry", "egress-binding"} {
		if reason, refusedIt := refused[id]; refusedIt {
			t.Errorf("%s has no admission condition and must not be refused: %s", id, reason)
		}
	}
}

func TestEverythingIsAdmittedWhenTheWorldAgrees(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	admitted, refused := g.Admissible(healthy())
	if len(refused) != 0 {
		t.Fatalf("a consistent world must admit every component, refused=%v", refused)
	}
	if len(admitted) != len(g.Order()) {
		t.Fatalf("admitted %d of %d components", len(admitted), len(g.Order()))
	}
}

// Two binaries whose accepted schema sets overlap can both hold Active while
// the database sits in the overlap, and the old one loses admission the
// moment the database moves past it. That transition is the point of keeping
// database.schema.tip and runtime.schema.compatibility as separate keys.
func TestRollingReplacementIsExpressible(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	oldBinary := healthy()
	oldBinary[KeyRuntimeSchemaCompatibility] = "55"
	newBinary := healthy()
	newBinary[KeyRuntimeSchemaCompatibility] = "55,56"

	for _, world := range []Observation{oldBinary, newBinary} {
		if err := g.Admit("runtime-orgd", world); err != nil {
			t.Fatalf("both binaries accept 55, so both must be admitted at 55: %v", err)
		}
	}

	oldBinary[KeyDatabaseSchemaTip] = "56"
	newBinary[KeyDatabaseSchemaTip] = "56"
	if err := g.Admit("runtime-orgd", oldBinary); err == nil {
		t.Fatal("the old binary must lose admission once the database moves to 56")
	} else if !strings.Contains(err.Error(), "is not in") {
		t.Fatalf("the refusal must say the tip is outside the accepted set: %v", err)
	}
	if err := g.Admit("runtime-orgd", newBinary); err != nil {
		t.Fatalf("the new binary accepts 56 and must stay admitted: %v", err)
	}
}

func TestAnUnobservedFactDeniesAdmissionRatherThanPassingIt(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	obs := healthy()
	delete(obs, KeyEgressBoundRevision)
	err = g.Admit("runtime-orgd", obs)
	if err == nil {
		t.Fatal("a fact nobody has observed must never be treated as satisfied")
	}
	if !strings.Contains(err.Error(), "not observed: canonical.egress.binding") {
		t.Fatalf("the refusal must name what was not observed: %v", err)
	}
}

func TestDivergenceIsReportedWithBothValues(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if converged, diverged := g.Converged(healthy()); !converged {
		t.Fatalf("a fleet running the promoted build is converged, got %v", diverged)
	}

	promoted := healthy()
	promoted[KeyRuntimeDesiredSHA] = "def456"
	converged, diverged := g.Converged(promoted)
	if converged {
		t.Fatal("a promotion the fleet has not picked up yet is divergence, not quiescence")
	}
	if len(diverged) != 1 || !strings.Contains(diverged[0], "def456") || !strings.Contains(diverged[0], "abc123") {
		t.Fatalf("divergence must carry both sides: %v", diverged)
	}

	// Divergence is not refusal. The fleet running the old build is still
	// entitled to be Active; that it owes a transition is a separate fact.
	if err := g.Admit("runtime-orgd", promoted); err != nil {
		t.Fatalf("divergence must not by itself deny admission: %v", err)
	}
}

func TestAdmittingOnAKeyTheComponentDoesNotDependOnIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{
			{Name: "mine", Composition: Exclusive},
			{Name: "theirs", Composition: Exclusive},
		},
		[]ComponentSpec{
			{ID: "a", Provides: []Key{"mine"}, Admits: []Predicate{Equal("mine", "theirs")}},
			{ID: "b", Provides: []Key{"theirs"}},
		},
	)
	if err == nil {
		t.Fatal("a component must not gate itself on a fact it has no declared relationship to")
	}
	if !strings.Contains(err.Error(), "neither requires nor provides") {
		t.Fatalf("the error must say why: %v", err)
	}
}

func TestAConvergencePairOwnedByOneComponentIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{
			{Name: "want", Composition: Exclusive},
			{Name: "have", Composition: Exclusive},
		},
		[]ComponentSpec{{ID: "a", Provides: []Key{"want", "have"}}},
		ConvergenceSpec{Desired: "want", Observed: "have"},
	)
	if err == nil {
		t.Fatal("one component stating both what should be and what is can always report itself quiescent")
	}
	if !strings.Contains(err.Error(), "different owners") {
		t.Fatalf("the error must say why: %v", err)
	}
}

func TestAConvergencePairComparingAKeyToItselfIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{{Name: "k", Composition: Exclusive}},
		[]ComponentSpec{{ID: "a", Provides: []Key{"k"}}},
		ConvergenceSpec{Desired: "k", Observed: "k"},
	)
	if err == nil || !strings.Contains(err.Error(), "never diverge") {
		t.Fatalf("a pair that can never diverge is a silent claim of quiescence: %v", err)
	}
}
