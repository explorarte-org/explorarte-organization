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
			"Do not ask the owner again for decisions already made in the current goal",
		},
	}

	got, err := buildCEOPlanInstructions(root, 16000)
	if err != nil {
		t.Fatalf("build CEO plan instructions: %v", err)
	}

	if !strings.HasPrefix(got, ceoPlanInstructionPrefix) {
		t.Fatal("CEO plan instructions lost the planning prefix")
	}

	// The current contract is exactly:
	//
	//     ceoPlanInstructionPrefix + authoritative owner-goal JSON
	//
	// There is intentionally no instruction suffix. Parsing the ENTIRE
	// remainder as one JSON value protects that contract: any text appended
	// after the JSON would make this unmarshal fail.
	body := strings.TrimPrefix(got, ceoPlanInstructionPrefix)

	var projected struct {
		Goal               string   `json:"goal"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}

	if err := json.Unmarshal([]byte(body), &projected); err != nil {
		t.Fatalf(
			"decode complete projected owner-goal payload: %v\npayload=%q",
			err,
			body,
		)
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

func TestBuildCEOPlanInstructionsFitsAtExactBoundary(t *testing.T) {
	root := TaskRecord{
		Instructions: "bounded owner goal",
		AcceptanceCriteria: []string{
			"criterion-a",
			"criterion-b",
		},
	}

	full, err := buildCEOPlanInstructions(root, 16000)
	if err != nil {
		t.Fatalf("build reference instructions: %v", err)
	}

	got, err := buildCEOPlanInstructions(root, len(full))
	if err != nil {
		t.Fatalf(
			"exact configured byte boundary should be accepted: %v",
			err,
		)
	}

	if got != full {
		t.Fatal("exact-boundary projection changed content")
	}

	_, err = buildCEOPlanInstructions(root, len(full)-1)
	if !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf(
			"one byte below required boundary should fail with ErrPlanTooLarge, got %v",
			err,
		)
	}
}
