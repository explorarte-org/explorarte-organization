package executive

import (
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// Local smoke #55 (root 2225): an analysis campaign's CEO plan named a design-review criterion no
// stage of that mode evaluates. The analysis CEO plan says what the mode has; the governed one is
// unchanged.
func TestTheAnalysisCEOPlanIsToldItsModeHasNoDesignStages(t *testing.T) {
	root := TaskRecord{ID: 1, Instructions: "goal", AcceptanceCriteria: []string{"c"}}
	analysis, err := buildCEOPlanInstructions(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(analysis, "EXECUTION_MODE: analysis_only.") || !strings.Contains(analysis, "no design review") {
		t.Fatalf("analysis CEO plan instructions lack the mode note: %q", analysis[:120])
	}
	root.Requirements = []RequirementRecord{{Key: designfreeze.RequirementKey}}
	governed, err := buildCEOPlanInstructions(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(governed, "EXECUTION_MODE") || !strings.HasPrefix(governed, ceoPlanInstructionPrefix) {
		t.Fatal("the governed CEO plan instructions changed")
	}
}

// Local smoke #57: the CEO referred servicios to a paper extract without copying it, and the
// department, which sees only its request, had nothing to work from.
func TestTheCEOIsToldDepartmentsSeeOnlyTheirRequest(t *testing.T) {
	got, err := buildCEOPlanInstructions(TaskRecord{ID: 1, Instructions: "goal"}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "each department sees only the request you write for it") || !strings.Contains(got, "verbatim") {
		t.Fatal("the CEO plan instructions do not say what a department can see")
	}
}
