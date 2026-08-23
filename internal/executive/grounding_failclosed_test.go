package executive

import (
	"errors"
	"testing"
)

// B7: an execution that should observe code, and cannot read its pin, must
// refuse -- not silently become an execution that observes none.
//
// The fail-open version built a context, ran the worker, and had every local
// component report success while the design went back to guessing. That is
// AUTONOMY-SMOKE-016 with the sensor unplugged.
//
// The two directions are tested together because the first version of this
// fix collapsed them: it refused for a deployment that has no promotion target
// at all, where being ungrounded is not a malfunction but the correct
// description of a system with no repository to observe.
func TestAGroundedPurposeRefusesRatherThanGoingBlind(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	root, err := fixture.orchestrator.tasks.GetTask(t.Context(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	worker := TaskRecord{ID: 99, TaskClass: "engineering.design", Instructions: "design something"}

	// A repository exists and the campaign is governed, but no pin was
	// recorded: what the designer would be reasoning about is unknown.
	_, _, err = fixture.orchestrator.repositoryGrounding(t.Context(), root, worker, PurposeDepartmentWorker)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a governed execution with no readable pin must refuse, got %v", err)
	}
}

func TestADeploymentWithNoRepositoryIsUngroundedNotBroken(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	root, err := fixture.orchestrator.tasks.GetTask(t.Context(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	// No promotion target: there is no repository for a design to be about.
	fixture.orchestrator.programTarget = nil
	worker := TaskRecord{ID: 99, TaskClass: "engineering.design", Instructions: "design something"}

	sha, query, err := fixture.orchestrator.repositoryGrounding(t.Context(), root, worker, PurposeDepartmentWorker)
	if err != nil {
		t.Fatalf("a deployment with no repository must be ungrounded, not refused: %v", err)
	}
	if sha != "" || query != "" {
		t.Fatalf("nothing to observe must produce no grounding, got %q / %q", sha, query)
	}
}

// And a purpose that never observes code is unaffected either way.
func TestAnUngroundedPurposeNeverRefuses(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	root, err := fixture.orchestrator.tasks.GetTask(t.Context(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []ExecutionPurpose{PurposeAdversarialReview, PurposeCEOPlan, PurposeCEOClosure} {
		if _, _, err := fixture.orchestrator.repositoryGrounding(t.Context(), root, TaskRecord{ID: 99}, purpose); err != nil {
			t.Fatalf("%s does not observe code and must never refuse for lack of a pin: %v", purpose, err)
		}
	}
}
