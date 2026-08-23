package executive

import (
	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"strings"
	"testing"
)

// The retrieval query is derived from the worker's instructions, and the host
// appends its own guidance to those instructions. AUTONOMY-SMOKE-017-R5
// measured what that costs: five of the eight incidental search terms that
// exhausted the file budget came from the egress rule the host itself added,
// crowding out the symbols the goal named. Telling a worker the rules must not
// change what it is allowed to see.
func TestHostGuidanceDoesNotShapeWhatTheWorkerCanSee(t *testing.T) {
	planned := "Diagnose how MaxDesignRounds and MaxDepartmentReplans are governed."
	orchestrator := &Orchestrator{programTarget: &fakeProgramTarget{sha: designSHA}}
	root := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey}}}

	instructions := orchestrator.workerInstructions(root, planned)
	if !strings.Contains(instructions, "cite it, do not reproduce it") {
		t.Fatal("the fixture never received the guidance, so it proves nothing")
	}

	query := withoutHostGuidance(instructions)
	if query != planned {
		t.Fatalf("host guidance survived into the retrieval query:\n%q", query)
	}
	for _, fromTheRule := range []string{"EVIDENCE", "PROHIBIDO", "Encoding", "Describing", "ALLOWED"} {
		if strings.Contains(query, fromTheRule) {
			t.Errorf("%q reaches the selector and competes for the search budget", fromTheRule)
		}
	}
}
