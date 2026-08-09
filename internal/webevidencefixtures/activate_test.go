package webevidencefixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestActivateMarksHostileWebPageRunnerReady(t *testing.T) {
	activated := Activate(fixtures.CatalogR30())
	if len(activated) != 14 {
		t.Fatalf("activated catalog has %d fixtures, want 14", len(activated))
	}
	for _, f := range activated {
		if err := f.Validate(); err != nil {
			t.Fatalf("activated fixture %s failed validation: %v", f.ID, err)
		}
		if f.ID == "r30-13-hostile-web-page" {
			if f.Status != fixtures.StatusRunnerReady {
				t.Fatalf("hostile web page fixture status=%s, want runner_ready", f.Status)
			}
			if _, ok := f.Scenario.(*WebEvidenceScenario); !ok {
				t.Fatalf("hostile web page fixture scenario type=%T, want *WebEvidenceScenario", f.Scenario)
			}
			continue
		}
		if f.Status == fixtures.StatusRunnerReady && f.RunnerKind == "web-evidence" {
			t.Fatalf("unexpected web-evidence fixture activated: %s", f.ID)
		}
	}
}

func TestWebEvidenceRunnerHostilePagePassesAllInvariants(t *testing.T) {
	activated := Activate(fixtures.CatalogR30())
	var target fixtures.Fixture
	for _, f := range activated {
		if f.ID == "r30-13-hostile-web-page" {
			target = f
		}
	}
	runner := WebEvidenceRunner{}
	if !runner.Supports(target) {
		t.Fatal("expected activated fixture to be supported")
	}
	outcome, err := runner.Run(context.Background(), target, "test-subject")
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Passed {
		t.Fatalf("expected fixture to pass its own invariants, got violated=%v notes=%s", outcome.ViolatedInvariants, outcome.Notes)
	}
	if outcome.Metrics["sanitization_findings"] < 1 {
		t.Fatalf("expected at least one sanitization finding, got metrics=%+v", outcome.Metrics)
	}
}

func TestWebEvidenceRunnerDetectsMissingExpectedPattern(t *testing.T) {
	activated := Activate(fixtures.CatalogR30())
	var target fixtures.Fixture
	for _, f := range activated {
		if f.ID == "r30-13-hostile-web-page" {
			target = f
		}
	}
	scenario := target.Scenario.(*WebEvidenceScenario)
	broken := *scenario
	broken.ExpectedFindingPattern = "a_pattern_that_will_never_match"
	target.Scenario = &broken

	runner := WebEvidenceRunner{}
	outcome, err := runner.Run(context.Background(), target, "test-subject")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Passed {
		t.Fatal("expected the mutated fixture (impossible expected pattern) to fail its own invariant")
	}
}

func TestRunSuiteRunsOnlyTheActivatedWebEvidenceFixture(t *testing.T) {
	runner := WebEvidenceRunner{}
	activated := Activate(fixtures.CatalogR30())
	outcomes, err := fixtures.RunSuite(context.Background(), runner, activated, "test-subject")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].FixtureID != "r30-13-hostile-web-page" {
		t.Fatalf("outcomes=%+v", outcomes)
	}
}
