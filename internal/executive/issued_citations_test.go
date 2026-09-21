package executive

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

// The citation contract has two halves. The validator half -- an accepted
// reference must be one the host issued -- was always enforced. These tests pin
// the half that was missing: a model is told, exactly, what it was issued.

func referencesOf(sources []SnapshotSource) []string {
	refs := make([]string, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, source.Reference)
	}
	sort.Strings(refs)
	return refs
}

// accepted_ref(ref) <=> ref is issued: whatever the validator accepts, the model
// was told, and nothing the model was not told is accepted.
func TestIssuedCitationsAreExactlyTheSetTheValidatorAccepts(t *testing.T) {
	ctx := context.Background()
	sources := snapshotWith()
	issued, err := issuedRepositoryCitations(ctx, sources, 1, designSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(issued) != 1 || issued[0] != realCite {
		t.Fatalf("issued = %v, want exactly the one excerpt the model was shown (%s)", issued, realCite)
	}
	orchestrator := &Orchestrator{}
	if invalid, err := orchestrator.VerifyEvidenceProvenance(ctx, sources, 1, designSHA, issued); err != nil || len(invalid) != 0 {
		t.Fatalf("an issued reference was rejected by the validator: %v (%v)", invalid, err)
	}
	// Everything the model was NOT issued -- dropped for budget, about another
	// commit, another kind of source, invented -- is rejected.
	notIssued := []string{droppedCit, staleCite, "rag://note/1", inventedCi}
	invalid, err := orchestrator.VerifyEvidenceProvenance(ctx, sources, 1, designSHA, notIssued)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), notIssued...)
	sort.Strings(want)
	if strings.Join(invalid, "|") != strings.Join(want, "|") {
		t.Fatalf("validator rejected %v, want every non-issued reference %v", invalid, want)
	}
}

// The three forms the production smoke's workers composed (roots 906, 939, 955,
// 974) because they were shown no handle: none is a reference the host issued.
func TestReferencesComposedByAWorkerAreNotIssuedAndAreRejected(t *testing.T) {
	ctx := context.Background()
	const sha = "c38da605ec41f52dc490fc405c1b8d8e5778fde6"
	issuedRef := "repository://explorarte-organization@" + sha + "/internal/identifiers/identifiers.go#L1-L34"
	sources := stubSnapshotSources{sources: []SnapshotSource{{Kind: "repository_evidence", Reference: issuedRef, Version: sha, Included: true}}}
	orchestrator := &Orchestrator{}
	composed := []string{
		"repository://internal/identifiers/identifiers.go#" + sha,
		"repository://" + sha + "/internal/identifiers/identifiers.go#L16",
		"repository://" + sha + "/internal/identifiers/identifiers.go#L20-L34",
	}
	invalid, err := orchestrator.VerifyEvidenceProvenance(ctx, sources, 1, sha, composed)
	if err != nil || len(invalid) != len(composed) {
		t.Fatalf("composed references: invalid = %v (%v); every one must be rejected", invalid, err)
	}
	// The issued reference, and a range inside it, are accepted.
	if invalid, err := orchestrator.VerifyEvidenceProvenance(ctx, sources, 1, sha, []string{
		issuedRef, "repository://explorarte-organization@" + sha + "/internal/identifiers/identifiers.go#L20-L34",
	}); err != nil || len(invalid) != 0 {
		t.Fatalf("an issued reference (or a range inside it) was rejected: %v (%v)", invalid, err)
	}
}

func TestIssuedCitationGuidanceStatesEveryIssuedReferenceExactly(t *testing.T) {
	refs := referencesOf(snapshotWithOverlappingFragments().sources)
	guidance := issuedCitationGuidance(refs)
	for _, ref := range refs {
		if !strings.Contains(guidance, "- "+ref+"\n") {
			t.Errorf("guidance does not list %s verbatim", ref)
		}
	}
	for _, rule := range []string{"ONLY repository references you may cite", "character for character", "Never compose", "&#x2F;", "unverified"} {
		if !strings.Contains(guidance, rule) {
			t.Errorf("guidance lacks %q", rule)
		}
	}
}

func TestIssuedCitationGuidanceWithNothingIssuedSaysSoAndAllowsEmptyEvidence(t *testing.T) {
	guidance := issuedCitationGuidance(nil)
	if !strings.Contains(guidance, "none") || !strings.Contains(guidance, "empty evidence list") {
		t.Fatalf("guidance = %q", guidance)
	}
	if strings.Contains(guidance, "repository://") {
		t.Fatal("guidance invented a reference when none was issued")
	}
}

func TestIssuedCitationGuidanceIsBounded(t *testing.T) {
	refs := make([]string, 0, maxIssuedCitationsListed+9)
	for i := 0; i < maxIssuedCitationsListed+9; i++ {
		refs = append(refs, "repository://r@"+designSHA+"/f"+strings.Repeat("x", i%7)+"/"+string(rune('a'+i%26))+".go#L1-L2")
	}
	sort.Strings(refs)
	guidance := issuedCitationGuidance(refs)
	listed := 0
	for _, line := range strings.Split(guidance, "\n") {
		if strings.HasPrefix(line, "- repository://") {
			listed++
		}
	}
	if listed != maxIssuedCitationsListed {
		t.Fatalf("listed %d references, want exactly the bound %d", listed, maxIssuedCitationsListed)
	}
	if !strings.Contains(guidance, "9 further issued references") {
		t.Fatalf("guidance does not say how many were omitted: %q", guidance[len(guidance)-200:])
	}
}

// Only executions that cite repository evidence, and only when grounded, carry
// the list: an ordinary campaign keeps its exact contract.
func TestIssuedCitationsAreAppendedOnlyWhereTheyAreCited(t *testing.T) {
	refs := []string{realCite}
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentWorker, PurposeDepartmentReview} {
		if got := withIssuedCitations("base", purpose, designSHA, refs); !strings.Contains(got, realCite) || !strings.HasPrefix(got, "base\n\n") {
			t.Errorf("%s: contract = %q", purpose, got)
		}
	}
	for _, purpose := range []ExecutionPurpose{PurposeCEOPlan, PurposeDepartmentPlan, PurposeCEOClosure, PurposeDesignAdjudication, PurposeAdversarialReview} {
		if got := withIssuedCitations("base", purpose, designSHA, refs); got != "base" {
			t.Errorf("%s: contract changed to %q", purpose, got)
		}
	}
	if got := withIssuedCitations("base", PurposeDepartmentWorker, "", refs); got != "base" {
		t.Errorf("an ungrounded execution's contract changed to %q", got)
	}
}

func TestIssuedRepositoryCitationsSurfaceSourceReadErrors(t *testing.T) {
	boom := errors.New("snapshot unreadable")
	if _, err := issuedRepositoryCitations(context.Background(), stubSnapshotSources{err: boom}, 1, designSHA); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read error, not a silently empty list", err)
	}
	if refs, err := issuedRepositoryCitations(context.Background(), nil, 1, designSHA); err != nil || refs != nil {
		t.Fatalf("no reader: %v, %v", refs, err)
	}
}

// END TO END, through the real orchestrator: every repository excerpt the
// worker's snapshot carries has its exact reference in the worker's execution
// contract (visible_to_model(excerpt) => the model is told its reference), and
// so does the department review that judges the worker's output.
func TestEveryIssuedExcerptReachesTheWorkersAndReviewersContract(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	if _, err := fixture.driveUntilStopped(t, 24); err != nil {
		t.Fatalf("drive: %v", err)
	}
	want := referencesOf(fullSupply())
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentWorker, PurposeDepartmentReview} {
		command, ok := fixture.commandFor(purpose)
		if !ok {
			t.Fatalf("%s never ran", purpose)
		}
		for _, ref := range want {
			if !strings.Contains(command.ExecutionContract, "- "+ref+"\n") {
				t.Errorf("%s contract does not carry issued reference %s", purpose, ref)
			}
		}
	}
	// The plan and the adjudication are not asked to cite: their contracts do not
	// carry the list.
	for _, purpose := range []ExecutionPurpose{PurposeCEOPlan, PurposeDepartmentPlan} {
		if command, ok := fixture.commandFor(purpose); ok && strings.Contains(command.ExecutionContract, "CITABLE REPOSITORY REFERENCES") {
			t.Errorf("%s carries the citable-reference list", purpose)
		}
	}
}

// The adjudicator cannot demand a reference the worker was never issued: its
// contract states the universe rule, and demands go through evidence_requirements,
// which the host probes against the pinned world before binding a round to them.
func TestAdjudicationContractBindsDemandsToTheIssuedUniverse(t *testing.T) {
	guidance := adjudicationEvidenceContractGuidance()
	for _, want := range []string{
		"Workers may cite only repository references the host issued to them",
		`"provide authorized repository references"`,
		"evidence_requirements entry",
		"the host verifies it against the pinned repository and issues the exact references to the next round",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("adjudication contract lacks %q", want)
		}
	}
}
