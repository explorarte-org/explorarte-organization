package executive

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The pin has to be a fact the real store will accept. A git SHA is 40 hex
// characters and the Tasks Engine's evidence digest is 64: writing the commit
// there would have been refused in production while passing every test,
// because the fakes did not reproduce that boundary.
func TestThePinIsRecordedWithADigestTheStoreAccepts(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	root := fixture.rootRecord(t)
	found := false
	for _, evidence := range root.Evidence {
		if evidence.Reference != DesignBaseSHAReference+"1" {
			continue
		}
		found = true
		if !sha256Hex.MatchString(evidence.Digest) {
			t.Fatalf("evidence digest %q is not a sha256; the store would refuse it", evidence.Digest)
		}
		if evidence.Digest == targetSHA {
			t.Fatal("the git commit was written as the evidence digest")
		}
		if sha, _ := evidence.Metadata["design_base_sha"].(string); sha != targetSHA {
			t.Fatalf("the commit must survive in metadata, got %q", sha)
		}
	}
	if !found {
		t.Fatal("no pin was recorded")
	}
}

// The pin must exist BEFORE anyone reasons, not after everyone has finished.
//
// Pinning at the freeze proved only that the mission would inherit a commit.
// It proved nothing about what the designers were reasoning over -- and once
// repository evidence is wired in, that is the entire question: a design is
// evidence about a repository only if the repository was fixed before anyone
// looked at it.
func TestTheWorldIsFixedBeforeTheFirstCognitiveCall(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)

	// One pass: enough to reach the first model call and no further.
	if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil {
		t.Fatal(err)
	}
	root := fixture.rootRecord(t)
	pinned := ""
	for _, evidence := range root.Evidence {
		if evidence.Reference == DesignBaseSHAReference+"1" {
			pinned, _ = evidence.Metadata["design_base_sha"].(string)
		}
	}
	if pinned == "" {
		t.Fatal("the campaign reached its first cognitive call with no world pinned: whatever it read, nobody can say which repository it was")
	}
	if calls := len(fixture.purposes()); calls == 0 {
		t.Fatal("the fixture did not reach a model call, so this proves nothing")
	}

	// And every later round keeps that world, whatever the target does.
	fixture.target.moveAfter(1, "9999999999999999999999999999999999999999")
	fixture.drive(t)
	after := ""
	for _, evidence := range fixture.rootRecord(t).Evidence {
		if evidence.Reference == DesignBaseSHAReference+"1" {
			after, _ = evidence.Metadata["design_base_sha"].(string)
		}
	}
	if after != pinned {
		t.Fatalf("the pinned world changed from %q to %q while the campaign was running", pinned, after)
	}
	// Which is exactly why the run stops rather than retargeting.
	if got := fixture.rootRecord(t); got.ReasonCode != ReasonWorldChangedSinceFreeze {
		t.Fatalf("a moved target must stop the run, got reason %q", got.ReasonCode)
	}
	if !strings.Contains(fixture.rootRecord(t).Reason, pinned) {
		t.Fatal("the reason must name the world the design was decided about")
	}
}

// Pinning the world is a rule about DESIGNS, not a tax on every cognitive act
// the organization performs.
//
// designBaseSHA consults the promotion target, so pinning unconditionally
// would have made a campaign that has nothing to do with the repository depend
// on git being reachable. "A design about code must fix its world before
// reasoning" must not quietly become "everything the organization thinks
// requires resolving a commit".
func TestACampaignWithNoDesignFreezeDoesNotDependOnTheRepository(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)

	// Strip the design-freeze requirement: this campaign is not governed by
	// a design, so it has no world to fix.
	root, err := fixture.orchestrator.tasks.GetTask(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	kept := root.Requirements[:0]
	for _, requirement := range root.Requirements {
		if requirement.Key != designfreeze.RequirementKey && requirement.Key != MissionRequirementKey {
			kept = append(kept, requirement)
		}
	}
	root.Requirements = kept
	fixture.tasks.tasks[root.ID] = root

	// And break the repository entirely.
	fixture.target.err = errors.New("promotion target unreachable")

	if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil {
		t.Fatalf("a campaign with no design freeze must not need the repository: %v", err)
	}
	if len(fixture.purposes()) == 0 {
		t.Fatal("the campaign did not reach a cognitive call, so this proves nothing")
	}
	for _, evidence := range fixture.rootRecord(t).Evidence {
		if evidence.Reference == DesignBaseSHAReference+"1" {
			t.Fatal("a campaign with no design freeze pinned a world it will never reason about")
		}
	}
}
