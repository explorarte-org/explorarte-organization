package codeexecutionfixtures

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

func TestGoBugFixSandboxDemonstratesRedToGreen(t *testing.T) {
	var target fixtures.Fixture
	for _, f := range Activate(fixtures.CatalogR30()) {
		if f.ID == fixtureGoBugFix {
			target = f
		}
	}
	outcome, err := (Runner{}).Run(context.Background(), target, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Passed {
		t.Fatalf("expected sandbox to pass its own invariants, got violated=%v notes=%s", outcome.ViolatedInvariants, outcome.Notes)
	}
	if !outcome.InvariantResults["test_fails_before_fix_is_applied"] {
		t.Fatal("expected the red state to be observed before the fix")
	}
	if !outcome.InvariantResults["test_passes_after_fix_is_applied"] {
		t.Fatal("expected the green state to be observed after the fix")
	}
}
