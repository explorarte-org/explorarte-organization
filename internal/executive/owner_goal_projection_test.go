package executive

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBuildCEOPlanInstructionsCarriesAuthoritativeOwnerGoal(t *testing.T) {
	root := TaskRecord{
		Instructions: "MASTER M2.1: design first, review, then implement Memory OS",
		AcceptanceCriteria: []string{
			"Design before implementation",
			"Preserve immutable experience memory",
		},
	}

	got, err := buildCEOPlanInstructions(root, 16000)
	if err != nil {
		t.Fatalf("build CEO plan instructions: %v", err)
	}

	if !strings.HasPrefix(got, ceoPlanInstructionPrefix) {
		t.Fatal("CEO plan instructions lost the planning prefix")
	}

	body := strings.TrimPrefix(got, ceoPlanInstructionPrefix)

	parts := strings.SplitN(body, ceoPlanInstructionSuffix, 2)
	if len(parts) != 2 {
		t.Fatal("CEO plan instructions lost the owner-decision policy suffix")
	}

	var projected struct {
		Goal               string   `json:"goal"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	if err := json.Unmarshal([]byte(parts[0]), &projected); err != nil {
		t.Fatalf("decode projected owner goal: %v", err)
	}

	if !strings.Contains(
		ceoPlanInstructionSuffix,
		"owner_decisions_required MUST be []",
	) {
		t.Fatal("CEO planning contract does not constrain ordinary owner escalation")
	}

	if !strings.Contains(
		ceoPlanInstructionSuffix,
		"GROK_REVIEW_UNAVAILABLE",
	) {
		t.Fatal("CEO planning contract lost the authorized Grok fallback")
	}

	if projected.Goal != root.Instructions {
		t.Fatalf(
			"owner goal drifted:\n got: %q\nwant: %q",
			projected.Goal,
			root.Instructions,
		)
	}

	if len(projected.AcceptanceCriteria) != len(root.AcceptanceCriteria) {
		t.Fatalf(
			"acceptance criteria count drifted: got=%d want=%d",
			len(projected.AcceptanceCriteria),
			len(root.AcceptanceCriteria),
		)
	}

	for i := range root.AcceptanceCriteria {
		if projected.AcceptanceCriteria[i] != root.AcceptanceCriteria[i] {
			t.Fatalf(
				"acceptance criterion %d drifted: got=%q want=%q",
				i,
				projected.AcceptanceCriteria[i],
				root.AcceptanceCriteria[i],
			)
		}
	}
}

func TestBuildCEOPlanInstructionsFailsClosedInsteadOfTruncatingOwnerGoal(t *testing.T) {
	root := TaskRecord{
		Instructions: strings.Repeat("x", 2048),
	}

	_, err := buildCEOPlanInstructions(root, 128)
	if !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("expected ErrPlanTooLarge, got %v", err)
	}
}
