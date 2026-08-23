package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-016 could not design a change to internal/executive because
// the design phase never sees the repository. Giving it eyes is only safe if
// what it sees is pinned: a design decided about one version of the code,
// reviewed against that version and adjudicated on that version, must be
// implemented against that same version.
//
// Reading the target's current head at mission time was the silent
// substitution -- a decision about S0 quietly became work on S1, and the
// substitution left no trace. It would have been right often enough to be
// trusted, which is what made it dangerous.

// R1 + R2: the commit is resolved once and never re-resolved, so every design
// round argues about the same repository.
func TestTheDesignBaseIsPinnedOnceForTheWholeCampaign(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	// The resolver has two roles and they must not be confused: it is the
	// SOURCE OF THE PIN once, and afterwards only an OBSERVATION of the
	// current world. So the property is not "called once" -- it is that
	// exactly one pin exists and nothing later replaces it.
	root := fixture.rootRecord(t)
	pins := []string{}
	for _, evidence := range root.Evidence {
		if evidence.Reference == DesignBaseSHAReference+"1" {
			sha, _ := evidence.Metadata["design_base_sha"].(string)
			pins = append(pins, sha)
		}
	}
	if len(pins) != 1 {
		t.Fatalf("%d pins recorded; a design episode must be about exactly one world", len(pins))
	}
	if pins[0] != targetSHA {
		t.Fatalf("pinned base sha=%q, want the target head at design time %q", pins[0], targetSHA)
	}
	// And re-reading it never resolves a new one, whatever the target does.
	fixture.target.moveAfter(0, "9999999999999999999999999999999999999999")
	again, err := fixture.orchestrator.frozenDesignBaseSHA(context.Background(), fixture.rootRecord(t))
	if err != nil {
		t.Fatal(err)
	}
	if again != targetSHA {
		t.Fatalf("re-reading the pin returned %q; a pinned world cannot drift", again)
	}
}

// R5 + R6: the frozen design carries the commit, and the mission takes it from
// there rather than resolving one of its own.
func TestTheMissionInheritsTheCommitTheDesignWasDecidedAbout(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	command, ok := fixture.provisioner.last()
	if !ok {
		t.Fatal("no mission was provisioned")
	}
	if command.Policy.BaseSHA != targetSHA {
		t.Fatalf("mission base_sha=%q, want the frozen design's %q", command.Policy.BaseSHA, targetSHA)
	}
	root := fixture.rootRecord(t)
	frozenCarries := false
	for _, evidence := range root.Evidence {
		if sha, _ := evidence.Metadata["design_base_sha"].(string); sha == targetSHA && evidence.Metadata["design_freeze_record"] != nil {
			frozenCarries = true
		}
	}
	if !frozenCarries {
		t.Fatal("the freeze must record the commit its decision was about")
	}
}

// R7: the target moving is a fact about the world, never permission to
// implement the decision somewhere else.
func TestAMovedTargetStopsTheRunInsteadOfRetargetingIt(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	// Freeze happens against the original head; the world then moves.
	fixture.target.moveAfter(1, "9999999999999999999999999999999999999999")
	fixture.drive(t)

	root := fixture.rootRecord(t)
	if root.Status != "blocked" || root.ReasonCode != ReasonWorldChangedSinceFreeze {
		t.Fatalf("root=%q reason=%q, want blocked on a changed world", root.Status, root.ReasonCode)
	}
	if !strings.Contains(root.Reason, targetSHA) {
		t.Fatalf("the reason must name the commit the design was decided about, got %q", root.Reason)
	}
	if command, ok := fixture.provisioner.last(); ok {
		t.Fatalf("a mission was provisioned at %q despite the world having moved", command.Policy.BaseSHA)
	}
}

// R12: a design whose world is unknown cannot be implemented. The mission
// phase reads the pin and never creates one, so its absence fails closed
// instead of silently resolving whatever the target points at.
func TestAMissionRefusesADesignWithNoPinnedWorld(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	orchestrator := fixture.orchestrator
	root, err := orchestrator.tasks.GetTask(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.frozenDesignBaseSHA(context.Background(), root); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an unpinned design must be refused, got %v", err)
	}
}
