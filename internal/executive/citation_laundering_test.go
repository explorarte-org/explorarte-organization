package executive

import (
	"context"
	"strings"
	"testing"
)

// Authorization belongs to a citation AND the model that used it.
//
// Verifying each deliverable against its own snapshot and then merging the
// results into one list throws away exactly the distinction the verification
// established: a claim by a designer who never saw a file would inherit the
// grounding of one who did. The verification was individual; the
// authorization has to stay individual too.
func TestACitationAuthorizedForOneDeliverableDoesNotAuthorizeAnother(t *testing.T) {
	orchestrator := &Orchestrator{}

	// Worker B saw the file. Worker A did not, and cites it anyway.
	sawIt := stubSnapshotSources{sources: []SnapshotSource{
		{Kind: "repository_evidence", Reference: realCite, Version: designSHA, Included: true},
	}}
	sawNothing := stubSnapshotSources{sources: []SnapshotSource{}}

	claim := "The validator already handles this, see " + realCite + "."

	forB, err := orchestrator.VerifyRepositoryCitations(context.Background(), sawIt, 22, designSHA, claim, 2, 202)
	if err != nil {
		t.Fatal(err)
	}
	if len(forB) != 1 {
		t.Fatalf("the deliverable that saw the file must be authorized to cite it, got %+v", forB)
	}
	if forB[0].TaskID != 2 || forB[0].InvocationID != 202 {
		t.Fatalf("a verified citation must name whose it is, got %+v", forB[0])
	}

	forA, err := orchestrator.VerifyRepositoryCitations(context.Background(), sawNothing, 11, designSHA, claim, 1, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(forA) != 0 {
		t.Fatalf("a deliverable that never saw the file must not be authorized to cite it, got %+v", forA)
	}

	// The two results must remain distinguishable. If the only thing carried
	// forward were the reference string, these two would be the same fact.
	if len(forB) > 0 && forB[0].Reference == realCite && len(forA) == 0 {
		if forB[0].TaskID == 0 {
			t.Fatal("without an owner, an authorized citation is indistinguishable from a laundered one")
		}
	}
}

// The bundle the reviewer receives must keep them apart.
func TestTheBundleAuthorizesPerDeliverableNotGlobally(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	var bundle string
	for _, task := range fixture.tasks.tasks {
		if task.TaskClass == TaskClassCoordinationAdversarialReview {
			bundle = task.Instructions
		}
	}
	if bundle == "" {
		t.Fatal("no adversarial review ran, so this proves nothing")
	}
	// The reviewer must be told that a reference authorized for one
	// deliverable does not authorize another's claim. Without that sentence
	// a per-deliverable structure is just a differently shaped union.
	for _, needed := range []string{
		"deliverables[].verified_repository_refs",
		"does not authorize a claim made by another",
	} {
		if !strings.Contains(bundle, needed) {
			t.Fatalf("the reviewer is not told that authorization is per deliverable: missing %q", needed)
		}
	}
}
