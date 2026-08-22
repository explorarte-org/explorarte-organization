package executive

import (
	"context"
	"errors"
	"testing"
)

const (
	designSHA  = "c30328eda491241fccb81b8c83feb8a5b1e6cc35"
	anotherSHA = "eedc79f4560701d59c80375bf7f5e19b2a6a8438"
	realCite   = "repository://explorarte-organization@c30328eda491241fccb81b8c83feb8a5b1e6cc35/internal/executive/validator.go#L52-L92"
	staleCite  = "repository://explorarte-organization@eedc79f4560701d59c80375bf7f5e19b2a6a8438/internal/executive/validator.go#L52-L92"
	droppedCit = "repository://explorarte-organization@c30328eda491241fccb81b8c83feb8a5b1e6cc35/internal/executive/orchestrator.go#L1-L40"
	inventedCi = "repository://explorarte-organization@c30328eda491241fccb81b8c83feb8a5b1e6cc35/internal/executive/imaginary.go#L1-L10"
)

type stubSnapshotSources struct {
	sources []SnapshotSource
	err     error
}

func (s stubSnapshotSources) SnapshotSources(context.Context, int64) ([]SnapshotSource, error) {
	return s.sources, s.err
}

func snapshotWith() stubSnapshotSources {
	return stubSnapshotSources{sources: []SnapshotSource{
		// What the designer actually saw.
		{Kind: "repository_evidence", Reference: realCite, Version: designSHA, Included: true},
		// Known to the context and dropped before the model saw it.
		{Kind: "repository_evidence", Reference: droppedCit, Version: designSHA, Included: false},
		// Evidence about a different repository entirely.
		{Kind: "repository_evidence", Reference: staleCite, Version: anotherSHA, Included: true},
		// Something else that happens to be untrusted data.
		{Kind: "rag_evidence", Reference: "rag://note/1", Version: "v1", Included: true},
	}}
}

// P11: a citation that was really in front of this model is verified.
func TestACitationTheModelActuallySawIsVerified(t *testing.T) {
	orchestrator := &Orchestrator{}
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), snapshotWith(), 7, designSHA,
		"The validator already rejects this, see "+realCite+".")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || verified[0].Reference != realCite {
		t.Fatalf("verified=%+v, want the citation the designer was given", verified)
	}
}

// P8: a citation nobody supplied is never verified, however plausible it
// looks. This is the failure AUTONOMY-SMOKE-016 produced by the dozen.
func TestAnInventedCitationIsNeverVerified(t *testing.T) {
	orchestrator := &Orchestrator{}
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), snapshotWith(), 7, designSHA,
		"As shown in "+inventedCi+", the helper is unused.")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 {
		t.Fatalf("an invented citation was verified: %+v", verified)
	}
	// And the claim is still visible as a claim, so a reviewer can say the
	// design rested on nothing rather than seeing no citation at all.
	claimed := RepositoryCitationsIn("As shown in " + inventedCi + ", the helper is unused.")
	if len(claimed) != 1 {
		t.Fatalf("the unverifiable claim must remain visible, got %v", claimed)
	}
	if describeUnverified(claimed, verified) == "" {
		t.Fatal("an unverifiable citation must be nameable in a finding")
	}
}

// P9: evidence about another commit is evidence about another repository.
func TestACitationOfAnotherCommitIsNeverVerified(t *testing.T) {
	orchestrator := &Orchestrator{}
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), snapshotWith(), 7, designSHA,
		"Per "+staleCite+" the behaviour is already correct.")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 {
		t.Fatalf("a citation of %s was verified for a design about %s: %+v", anotherSHA, designSHA, verified)
	}
}

// P10: a source that was known and then dropped is not something the model
// read. This is the one that would otherwise pass: the reference is real, the
// commit is right, and the designer never saw a line of it.
func TestADroppedSourceIsNeverVerified(t *testing.T) {
	orchestrator := &Orchestrator{}
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), snapshotWith(), 7, designSHA,
		"See "+droppedCit+" for the current shape.")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 {
		t.Fatalf("evidence omitted before the model saw it was verified: %+v", verified)
	}
}

// The verifier answers provenance and nothing else. It must not decide whether
// a design's reasoning is correct -- that is the reviewer's work, and a host
// ruling on it would replace an adversarial judgement with a mechanical one.
func TestTheVerifierOnlyAnswersProvenance(t *testing.T) {
	orchestrator := &Orchestrator{}
	// A claim that is obviously false, resting on a genuine citation.
	verified, err := orchestrator.VerifyRepositoryCitations(context.Background(), snapshotWith(), 7, designSHA,
		"This file is empty and does nothing, see "+realCite+".")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 {
		t.Fatal("the host must confirm the citation was real without judging the claim made about it")
	}
}

// A verifier that cannot read the snapshot must not silently verify nothing:
// "no citations were genuine" and "I could not check" are different answers.
func TestAnUnreadableSnapshotIsAnError(t *testing.T) {
	orchestrator := &Orchestrator{}
	_, err := orchestrator.VerifyRepositoryCitations(context.Background(),
		stubSnapshotSources{err: errors.New("snapshot unavailable")}, 7, designSHA, "see "+realCite)
	if err == nil {
		t.Fatal("an unreadable snapshot must not read as a design with no valid citations")
	}
}
