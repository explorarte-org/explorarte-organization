package fixtures

import (
	"context"
	"testing"
)

func runnerReadyFixture(t *testing.T, id string) Fixture {
	t.Helper()
	for _, f := range CatalogR30() {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("fixture %s not found in catalog", id)
	return Fixture{}
}

func TestDecisionGraphRunnerBudgetExhaustionFailsAtExpectedReservation(t *testing.T) {
	f := runnerReadyFixture(t, "r30-08-budget-exhaustion")
	runner := DecisionGraphRunner{}
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
	f := runnerReadyFixture(t, "r30-11-dag-cycles-depth-terminal-evidence")
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
	f := runnerReadyFixture(t, "r30-12-contradictory-evidence-non-selection")
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
	// test above is not vacuously true.
	f := runnerReadyFixture(t, "r30-11-dag-cycles-depth-terminal-evidence")
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
	outcomes, err := RunSuite(context.Background(), runner, CatalogR30(), "test-subject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) == 0 {
		t.Fatal("expected at least one outcome from runner-ready fixtures")
	}
	for _, outcome := range outcomes {
		if !outcome.Passed {
			t.Fatalf("fixture %s failed: %v", outcome.FixtureID, outcome.ViolatedInvariants)
		}
	}
}

func TestDeterministicSourceIsReproducibleAndSubjectSensitive(t *testing.T) {
	f := runnerReadyFixture(t, "r30-08-budget-exhaustion")
	a1 := DeterministicSource(f, "lexical").Int63()
	a2 := DeterministicSource(f, "lexical").Int63()
	if a1 != a2 {
		t.Fatalf("same fixture+subject produced different sequences: %d vs %d", a1, a2)
	}
	b := DeterministicSource(f, "gemini-hybrid").Int63()
	if a1 == b {
		t.Fatal("different subjects unexpectedly produced the same sequence")
	}
}
