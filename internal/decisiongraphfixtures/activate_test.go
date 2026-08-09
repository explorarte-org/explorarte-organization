package decisiongraphfixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func activatedFixture(t *testing.T, id string) fixtures.Fixture {
	t.Helper()
	for _, f := range Activate(fixtures.CatalogR30()) {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("fixture %s not found after Activate", id)
	return fixtures.Fixture{}
}

func TestActivateMarksExactlyTheKnownDecisionGraphFixturesRunnerReady(t *testing.T) {
	activated := Activate(fixtures.CatalogR30())
	if len(activated) != 14 {
		t.Fatalf("activated catalog has %d fixtures, want 14", len(activated))
	}
	wantReady := map[string]bool{
		"r30-08-budget-exhaustion":                    true,
		"r30-11-dag-cycles-depth-terminal-evidence":   true,
		"r30-12-contradictory-evidence-non-selection": true,
	}
	for _, f := range activated {
		if err := f.Validate(); err != nil {
			t.Fatalf("activated fixture %s failed validation: %v", f.ID, err)
		}
		ready := f.Status == fixtures.StatusRunnerReady
		if ready != wantReady[f.ID] {
			t.Fatalf("fixture %s status=%s, want runner_ready=%v", f.ID, f.Status, wantReady[f.ID])
		}
		if ready {
			if _, ok := f.Scenario.(*DecisionGraphScenario); !ok {
				t.Fatalf("fixture %s is runner_ready but its scenario is not *DecisionGraphScenario: %T", f.ID, f.Scenario)
			}
		}
	}
}

func TestActivateDoesNotMutateTheInputCatalog(t *testing.T) {
	original := fixtures.CatalogR30()
	_ = Activate(original)
	for _, f := range original {
		if f.Status != fixtures.StatusPending {
			t.Fatalf("Activate mutated the caller's slice: fixture %s status=%s", f.ID, f.Status)
		}
	}
}

func TestDecisionGraphRunnerBudgetExhaustionFailsAtExpectedReservation(t *testing.T) {
	f := activatedFixture(t, "r30-08-budget-exhaustion")
	runner := DecisionGraphRunner{}
	if !runner.Supports(f) {
		t.Fatal("expected activated fixture to be supported")
	}
	outcome, err := runner.Run(context.Background(), f, "test-subject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.Passed {
		t.Fatalf("expected fixture to pass its own invariants, got violated=%v notes=%s", outcome.ViolatedInvariants, outcome.Notes)
	}
	if !outcome.InvariantResults["budget_exhausts_at_the_expected_reservation_and_not_before"] {
		t.Fatal("expected budget exhaustion invariant to hold")
	}
}

func TestDecisionGraphRunnerDAGRejectsCycleAndBadTerminal(t *testing.T) {
	f := activatedFixture(t, "r30-11-dag-cycles-depth-terminal-evidence")
	runner := DecisionGraphRunner{}
	outcome, err := runner.Run(context.Background(), f, "test-subject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.Passed {
		t.Fatalf("expected fixture to pass its own invariants, got violated=%v notes=%s", outcome.ViolatedInvariants, outcome.Notes)
	}
	if outcome.Metrics["max_depth"] != 4 {
		t.Fatalf("max_depth=%v want 4", outcome.Metrics["max_depth"])
	}
	if !outcome.InvariantResults["dependency_cycle_is_rejected"] {
		t.Fatal("expected the cyclic edge addition to be rejected")
	}
	if !outcome.InvariantResults["terminal_decision_never_closes_without_real_evidence"] {
		t.Fatal("expected the 'unknown'-labeled terminal decision to be rejected")
	}
}

func TestDecisionGraphRunnerContradictedEvidenceNeverSelects(t *testing.T) {
	f := activatedFixture(t, "r30-12-contradictory-evidence-non-selection")
	runner := DecisionGraphRunner{}
	outcome, err := runner.Run(context.Background(), f, "test-subject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.Passed {
		t.Fatalf("expected fixture to pass its own invariants, got violated=%v notes=%s", outcome.ViolatedInvariants, outcome.Notes)
	}
	if !outcome.InvariantResults["terminal_decision_never_closes_without_real_evidence"] {
		t.Fatal("expected the 'contradicted'-labeled terminal decision to be rejected")
	}
}

func TestDecisionGraphRunnerDetectsARegressedInvariant(t *testing.T) {
	// Sanity check that Run can actually fail: flip the fixture so the
	// terminal decision it exercises WOULD structurally pass (label
	// verified) but the fixture still expects rejection — this proves the
	// tests above are not vacuously true.
	f := activatedFixture(t, "r30-11-dag-cycles-depth-terminal-evidence")
	scenario := f.Scenario.(*DecisionGraphScenario)
	broken := *scenario
	brokenTerminal := *scenario.TerminalDecision
	brokenTerminal.Label = "verified"
	broken.TerminalDecision = &brokenTerminal
	f.Scenario = &broken

	runner := DecisionGraphRunner{}
	outcome, err := runner.Run(context.Background(), f, "test-subject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Passed {
		t.Fatal("expected the mutated fixture (verified label but ShouldPass=false) to fail its own invariant")
	}
}

func TestRunSuiteSkipsUnsupportedFixturesAndRunsSupportedOnes(t *testing.T) {
	runner := DecisionGraphRunner{}
	activated := Activate(fixtures.CatalogR30())
	outcomes, err := fixtures.RunSuite(context.Background(), runner, activated, "test-subject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("expected exactly 3 runner-ready outcomes, got %d", len(outcomes))
	}
	for _, outcome := range outcomes {
		if !outcome.Passed {
			t.Fatalf("fixture %s failed: %v", outcome.FixtureID, outcome.ViolatedInvariants)
		}
	}
}

func TestDeterministicSourceIsReproducibleAndSubjectSensitive(t *testing.T) {
	f := activatedFixture(t, "r30-08-budget-exhaustion")
	a1 := fixtures.DeterministicSource(f, "lexical").Int63()
	a2 := fixtures.DeterministicSource(f, "lexical").Int63()
	if a1 != a2 {
		t.Fatalf("same fixture+subject produced different sequences: %d vs %d", a1, a2)
	}
	b := fixtures.DeterministicSource(f, "gemini-hybrid").Int63()
	if a1 == b {
		t.Fatal("different subjects unexpectedly produced the same sequence")
	}
}
