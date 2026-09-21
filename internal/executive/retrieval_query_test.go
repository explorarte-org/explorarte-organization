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

// The host's statement of campaign state (approved, by whom) is for the
// planners. It must not shape what the worker can see: it is stripped from the
// retrieval query by its exact markers, and only by them.
func TestHostCampaignStateDoesNotShapeRetrieval(t *testing.T) {
	statement := WrapHostCampaignState("CAMPAIGN STATE: APPROVED_FOR_EXECUTION\nOwner approval 6 by empresa/human. Do not request approval again.")
	goal := "Diagnose how MaxDesignRounds is governed."
	query := withoutHostCampaignState(statement + "\n\n" + goal)
	if query != goal {
		t.Fatalf("query = %q, want only the goal", query)
	}
	for _, fromTheStatement := range []string{"APPROVED_FOR_EXECUTION", "CAMPAIGN", "approval", "empresa"} {
		if strings.Contains(query, fromTheStatement) {
			t.Errorf("%q reaches the selector", fromTheStatement)
		}
	}
	// Free text that merely talks about a draft is the owner's, and stays.
	owner := "The campaign remains a draft until owner approval; touch MaxDesignRounds."
	if got := withoutHostCampaignState(owner); got != owner {
		t.Fatalf("free text was altered: %q", got)
	}
	// An unterminated marker removes nothing.
	open := HostCampaignStateBegin + "\nnever closed\n" + goal
	if got := withoutHostCampaignState(open); got != strings.TrimSpace(open) {
		t.Fatalf("an unterminated marker truncated the goal: %q", got)
	}
}

// END TO END: a root whose instructions open with the host's campaign state
// searches the repository for what the goal names, and for nothing the host's
// statement contains.
func TestPromotedRootsStateStatementNeverReachesTheRepositoryQuery(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.tasks.mu.Lock()
	root := fixture.tasks.tasks[fixture.root]
	root.Instructions = WrapHostCampaignState("Campaign state: APPROVED_FOR_EXECUTION. Owner approval 6 by empresa/human. Do not request approval again.") + "\n\n" + root.Instructions
	fixture.tasks.tasks[fixture.root] = root
	fixture.tasks.mu.Unlock()

	if _, err := fixture.driveUntilStopped(t, 24); err != nil {
		t.Fatalf("drive: %v", err)
	}
	command, ok := fixture.commandFor(PurposeDepartmentWorker)
	if !ok {
		t.Fatal("no worker ran")
	}
	request, recorded := fixture.harness.contexts.requests[command.Context.ID]
	if !recorded || strings.TrimSpace(request.RepositoryQuery) == "" {
		t.Fatalf("no repository query was recorded for snapshot %d", command.Context.ID)
	}
	for _, fromTheStatement := range []string{"APPROVED_FOR_EXECUTION", "Owner approval", "Do not request approval again"} {
		if strings.Contains(request.RepositoryQuery, fromTheStatement) {
			t.Errorf("%q from the host's state statement reached the repository query:\n%s", fromTheStatement, request.RepositoryQuery)
		}
	}
	if !strings.Contains(request.RepositoryQuery, "M2.1") {
		t.Errorf("the goal itself no longer drives retrieval:\n%s", request.RepositoryQuery)
	}
}
