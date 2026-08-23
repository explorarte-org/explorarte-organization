package executive

import (
	"errors"
	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"strings"
	"testing"
)

// A boundary the enforcer knows and the worker does not is a trap, not a
// safety property. AUTONOMY-SMOKE-017-R2 was refused for reproducing a line
// of budget.go that nothing had ever told the designer it could not copy.

func TestAWorkerThatCanSeeTheRepositoryIsToldTheRule(t *testing.T) {
	orchestrator := &Orchestrator{programTarget: &fakeProgramTarget{sha: designSHA}}
	root := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey}}}
	planned := "Diagnose how the two limits are governed."

	instructions := orchestrator.workerInstructions(root, planned)
	if !strings.Contains(instructions, planned) {
		t.Fatal("the host dropped the plan's own instruction")
	}
	for _, required := range []string{"cite it, do not reproduce it", "repository://", "refused as a whole"} {
		if !strings.Contains(instructions, required) {
			t.Fatalf("the rule does not state %q, so a worker could follow it and still be refused", required)
		}
	}
}

// A campaign with no repository has nothing to say about how to use one, and
// spending a worker's context on an inapplicable rule is not free.
func TestAWorkerThatSeesNoRepositoryIsNotToldTheRule(t *testing.T) {
	planned := "Write the note."
	governed := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey}}}

	ungoverned := (&Orchestrator{programTarget: &fakeProgramTarget{sha: designSHA}}).
		workerInstructions(TaskRecord{}, planned)
	noTarget := (&Orchestrator{}).workerInstructions(governed, planned)

	for name, instructions := range map[string]string{"no design freeze": ungoverned, "no promotion target": noTarget} {
		if instructions != planned {
			t.Fatalf("%s: the host added a repository rule to a campaign that observes no repository", name)
		}
	}
}

// The refusal must name which evidence was reproduced. A path, a line range
// and a commit are provenance metadata and may cross the boundary the source
// text may not -- and without them the match has to be reconstructed by hand
// from the database, which is what this incident actually cost.
func TestTheRefusalNamesTheCitationItReproduced(t *testing.T) {
	const reference = "repository://explorarte-organization/internal/executive/budget.go#L50-L60"
	body := "maxLeader := (2*departments + 2*l.MaxDepartmentReplans) * governedTaskAttempts"
	err := DeclassifyCandidate("The design keeps "+body, []OrganizationalSource{{Reference: reference, Content: body}})
	if !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("a verbatim copy was not refused: %v", err)
	}
	if !strings.Contains(err.Error(), reference) {
		t.Fatalf("the refusal does not say which citation was reproduced: %v", err)
	}
	if strings.Contains(err.Error(), "governedTaskAttempts") {
		t.Fatalf("the refusal quoted the source it exists to keep from travelling: %v", err)
	}
}
