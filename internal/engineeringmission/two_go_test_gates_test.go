package engineeringmission

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// missionplan.Derive runs GO_TEST twice when a mission changes Go packages: the changed packages by
// name, then the whole module, with a gate for each. Both must be satisfied, each by its own check.
func TestBothGoTestGatesMustBeSatisfiedByTheirOwnChecks(t *testing.T) {
	policy := MissionPolicy{RequiredGates: []RequiredGate{
		{Type: GateTest, Packages: []string{"./internal/identifiers/..."}},
		{Type: GateTest},
	}}
	both := attemptEvidence(1, 100, "GO_TEST", []string{"./internal/identifiers/..."}, false, false, true)
	checks := both.Metadata["checks_run"].([]any)
	both.Metadata["checks_run"] = append(checks, map[string]any{"type": "GO_TEST", "success": true, "race": false, "integration": false})
	svc := Service{Tasks: taskDetailStub{detail: tasks.TaskDetail{Evidence: []tasks.Evidence{both}}}}
	if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err != nil {
		t.Fatalf("both runs recorded, yet refused: %v", err)
	}
	for name, evidence := range map[string]tasks.Evidence{
		"only the literal run": attemptEvidence(1, 100, "GO_TEST", []string{"./internal/identifiers/..."}, false, false, true),
		"only the module run":  attemptEvidence(1, 100, "GO_TEST", nil, false, false, true),
	} {
		svc := Service{Tasks: taskDetailStub{detail: tasks.TaskDetail{Evidence: []tasks.Evidence{evidence}}}}
		if err := svc.VerifyRequiredGates(context.Background(), 1, 100, policy); err == nil {
			t.Errorf("%s satisfied both GO_TEST gates", name)
		}
	}
}
