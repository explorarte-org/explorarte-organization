package executive

import (
	"context"
	"errors"
	"testing"
)

// SELF-EVIDENCE-PROVENANCE-002.
//
// refsAreAllStructured (worker_result.go) answers whether a worker-result/v2
// document is internally coherent. ValidateEvidenceStructure
// (evidence_structure.go) answers whether a worker satisfied what was
// REQUIRED. Neither answers a third question: does a reference a worker (or
// reviewer) voluntarily offered, beyond anything required, actually name
// something the host verified was shown to THIS invocation?
//
// AUTONOMY-SMOKE-017-R17-V3's task 34 had zero EvidenceRequirements and
// offered "task:34" as evidence: structurally valid after
// fix/worker-result-v2-structural-contract, and never checked against
// anything real, because suppliedEvidence/ValidateEvidenceStructure are both
// gated on required != []. VerifyEvidenceProvenance is the missing check,
// deliberately unconditional on requiredness: requiredness decides what MUST
// be grounded, not whether a reference offered beyond that is real.
//
// These tests reuse repository_citation_verifier_test.go's fixtures
// (snapshotWith, designSHA, realCite, staleCite, droppedCit, inventedCi) --
// the "genuine" set both functions check against is the same set, built the
// same way, because a reference the host never showed is inadmissible for
// exactly the same reason whether it appears in free prose or in a
// structured evidence_refs entry.

// GUARD -- a reference actually shown to this invocation is admissible, even
// though nothing required it. Requiredness and provenance are orthogonal.
func TestAGenuinelyShownCitationIsAdmissibleEvenWhenNothingRequiredIt(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, designSHA, []string{realCite})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 0 {
		t.Fatalf("a genuinely shown citation was rejected: %v", invalid)
	}
}

// GUARD -- THE ATTACK, verbatim: a bare task self-reference offered as
// evidence, with no repository citation behind it at all. It is rejected for
// what it is -- not real -- not because it starts with "task:".
func TestASelfTaskReferenceIsNeverAdmissibleAsEvidence(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, designSHA, []string{"task:34"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 || invalid[0] != "task:34" {
		t.Fatalf("a self task reference was accepted as evidence: %v", invalid)
	}
}

// GUARD -- a syntactically plausible but invented repository:// URI fails
// for the same reason a bare self-reference does: neither traces to
// anything the host actually showed. This is deliberately not a namespace
// check -- see VerifyEvidenceProvenance's doc comment.
func TestAnInventedRepositoryURIIsNeverAdmissibleAsEvidence(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, designSHA, []string{inventedCi})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 || invalid[0] != inventedCi {
		t.Fatalf("an invented repository citation was accepted as evidence: %v", invalid)
	}
}

// GUARD -- no evidence offered needs no provenance: an empty offer trivially
// satisfies both requiredness and provenance.
func TestNoOfferedEvidenceNeedsNoProvenance(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, designSHA, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 0 {
		t.Fatalf("an empty offer was rejected: %v", invalid)
	}
}

// GUARD -- an ungrounded campaign (no baseSHA at all) has nothing a model
// could legitimately cite; ANY offered reference must fail, not merely the
// ones shaped like a self-citation. This is the exact shape of R17-v3's
// campaign, which was never repository-grounded at all.
func TestAnUngroundedCampaignAdmitsNoVoluntaryEvidence(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, "", []string{"task:34", realCite})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 2 {
		t.Fatalf("an ungrounded campaign admitted a reference: %v", invalid)
	}
}

// GUARD -- a source known and then dropped before reaching the model
// (Included: false) is not something the model read -- the same rule
// VerifyRepositoryCitations already enforces. Provenance shares the SAME
// genuine set, not a laxer one.
func TestADroppedSourceIsNotAdmissibleEvidenceEither(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, designSHA, []string{droppedCit})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a dropped source was accepted as evidence: %v", invalid)
	}
}

// GUARD -- evidence about another commit is evidence about another
// repository, exactly as VerifyRepositoryCitations already treats it.
func TestACitationOfAnotherCommitIsNotAdmissibleEvidenceEither(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), snapshotWith(), 7, designSHA, []string{staleCite})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a citation of a different commit was accepted as evidence: %v", invalid)
	}
}

// GUARD -- an unreadable snapshot must not read as "nothing was genuine";
// the caller must be told it could not check, not told the answer is no.
func TestAnUnreadableSnapshotFailsProvenanceClosed(t *testing.T) {
	orchestrator := &Orchestrator{}
	_, err := orchestrator.VerifyEvidenceProvenance(context.Background(),
		stubSnapshotSources{err: errors.New("snapshot unavailable")}, 7, designSHA, []string{realCite})
	if err == nil {
		t.Fatal("an unreadable snapshot must not read as a world with no genuine evidence")
	}
}

// GUARD -- VerifyRepositoryCitations, refactored to share
// genuineRepositoryCitations with the new function, must keep its exact
// existing behavior. This is the regression proof that sharing the "what did
// the host actually show" logic changed nothing observable about the
// free-text verifier the design-freeze path already depends on.
func TestVerifyRepositoryCitationsIsUnchangedByTheSharedHelper(t *testing.T) {
	orchestrator := &Orchestrator{}
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), snapshotWith(), 7, designSHA,
		"See "+realCite+".", 42, 99, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || verified[0].Reference != realCite {
		t.Fatalf("VerifyRepositoryCitations regressed: %+v", verified)
	}
}
