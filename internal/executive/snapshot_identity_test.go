package executive

import (
	"context"
	"testing"
)

// B3: stubSnapshotSources returned the same segments for every ID, so no test
// could tell snapshot 7 from snapshot 99 -- or one campaign's context from
// another's. The verifier looked identity-aware and was never asked an
// identity question.
//
// This is two DIFFERENT snapshots, keyed by ID, which is the only shape in
// which "the model saw R" can be false.
type perSnapshotSources map[int64][]SnapshotSource

func (p perSnapshotSources) SnapshotSources(_ context.Context, id int64) ([]SnapshotSource, error) {
	return p[id], nil
}

func TestACitationIsOnlyVerifiedAgainstItsOwnSnapshot(t *testing.T) {
	const otherCite = "repository://explorarte-organization@" + designSHA + "/internal/executive/orchestrator.go#L100-L140"
	sources := perSnapshotSources{
		// Snapshot 1: what this invocation was shown.
		1: {{Kind: "repository_evidence", Reference: realCite, Version: designSHA, Included: true}},
		// Snapshot 2: a different execution, containing a different excerpt.
		2: {{Kind: "repository_evidence", Reference: otherCite, Version: designSHA, Included: true}},
	}
	orchestrator := &Orchestrator{}

	// The claim cites what snapshot 2 held, while the invocation points at
	// snapshot 1. Nothing here may verify.
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), sources, 1, designSHA,
		"The orchestrator already does this, see "+otherCite+".", 2, 202, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 {
		t.Fatalf("a citation from another execution's snapshot was verified: %+v", verified)
	}

	// The same claim, from the execution that actually held it, verifies.
	verified, err = orchestrator.VerifyRepositoryCitations(context.Background(), sources, 2, designSHA,
		"The orchestrator already does this, see "+otherCite+".", 3, 303, "d2")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || verified[0].Reference != otherCite {
		t.Fatalf("the execution that was shown the excerpt must be able to cite it, got %+v", verified)
	}
	if verified[0].InvocationID != 303 || verified[0].ResultDigest != "d2" {
		t.Fatalf("the citation must carry its own owner, got %+v", verified[0])
	}

	// And an empty snapshot verifies nothing, which is how a wrong ID fails.
	verified, err = orchestrator.VerifyRepositoryCitations(context.Background(), sources, 99, designSHA,
		"see "+realCite, 4, 404, "d3")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 {
		t.Fatalf("a snapshot that holds nothing must authorize nothing, got %+v", verified)
	}
}
