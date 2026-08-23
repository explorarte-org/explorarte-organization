package executive

import (
	"testing"
)

// B4: the test named for R2 never moved the target, so it would have passed
// just as well if grounding re-resolved the promotion target on every round.
// fakeProgramTarget.calls() existed precisely to tell those apart and was
// never asserted.
//
// This moves the world after the pin and then measures the thing that matters:
// what every grounded execution was actually told to look at.
func TestNoRoundObservesAWorldOtherThanThePin(t *testing.T) {
	fixture, recorder := newGroundingFixture(t)

	// One pass to pin, then the repository moves under the campaign.
	if _, err := fixture.orchestrator.Resume(t.Context(), fixture.root); err != nil {
		t.Fatal(err)
	}
	fixture.target.moveAfter(1, "9999999999999999999999999999999999999999")
	fixture.drive(t)

	grounded := 0
	for _, request := range recorder.requests {
		if request.RepositoryBaseSHA == "" {
			continue
		}
		grounded++
		if request.RepositoryBaseSHA != targetSHA {
			t.Fatalf("%s was pointed at %q after the world moved; every round must argue about %q",
				request.ExecutionPurpose, request.RepositoryBaseSHA, targetSHA)
		}
	}
	if grounded == 0 {
		t.Fatal("no grounded execution ran after the move, so this proves nothing")
	}

	// The resolver is consulted again -- that is the mission-phase
	// OBSERVATION of the current world -- but it never becomes the world a
	// design reasons about. The distinction between those two roles is the
	// property; "called once" would be the wrong assertion.
	if fixture.target.calls() < 2 {
		t.Fatal("the fixture never observed the moved world, so the assertion above is vacuous")
	}
	if got := fixture.rootRecord(t).ReasonCode; got != ReasonWorldChangedSinceFreeze {
		t.Fatalf("a moved target must stop the run at the mission, got %q", got)
	}
}
