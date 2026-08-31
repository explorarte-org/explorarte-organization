package executive

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TYPED-EVIDENCE-VISIBILITY-FIX-005.
//
// VerifyEvidenceProvenance already answers CITABLE correctly: repository
// evidence genuinely shown is the only admissible class, and R17-v4's task
// 11992 proved organizational-context citations are rejected regardless of
// how real the underlying document is. What it does not yet answer honestly
// is VISIBLE: describeInadmissibleReferences' "known" map is built only from
// SnapshotSource.Reference -- one identity per whole segment -- so a
// reference genuinely embedded INSIDE a segment's structured content (an
// executive-evidence bundle's own model-invocation:<id> entries, attached by
// the host to the reviewing task before dispatch) is indistinguishable from
// one that was never shown at all. R17-v5's task 12512 proved this: it cited
// model-invocation:21-24, verbatim from a bundle the host itself attached and
// rendered, and was told "cannot verify was shown" -- false.
//
// These tests pin three properties as independent:
//
//	VISIBLE       -- was this exact identifier supplied to this execution?
//	CITABLE       -- may the model name it in evidence_refs/evidence?
//	AUTHORITATIVE -- may it satisfy/ground a requirement or verdict?
//
// AUTHORITATIVE is a (task, requirement, reference) triple, not a property of
// the ref string: the SAME model-invocation:21 satisfies a requirement on its
// OWNING task and carries requirement_id=NULL when echoed into another task's
// bundle. Nothing here changes VerifyEvidenceProvenance's accept/reject
// decision -- repository_evidence remains the only citable class. Only the
// diagnostic (and, for department review, the guidance offered before
// generation) changes.
//
// TEST B is the RED case: it calls describeInadmissibleReferences with
// today's signature, which has no way to see a task's own attached evidence,
// and asserts the answer must NOT be the false "cannot verify was shown" --
// which is exactly what today's code produces. Every other test here already
// holds against BASE_SHA and stays true after the fix; B is the one this
// commit is written to fail.

func taskRef(taskID int64) string {
	return "task:" + strconv.FormatInt(taskID, 10)
}

func snapshotWithTaskContext(taskID int64, canonicalRef string) stubSnapshotSources {
	base := snapshotWith()
	sources := append([]SnapshotSource(nil), base.sources...)
	sources = append(sources,
		SnapshotSource{Kind: "task_context", Reference: taskRef(taskID), Version: "task.v1:1:x", Included: true},
	)
	if canonicalRef != "" {
		sources = append(sources, SnapshotSource{Kind: "canonical_document", Reference: canonicalRef, Version: "v1", Included: true})
	}
	return stubSnapshotSources{sources: sources}
}

const currentReviewTaskID int64 = 12512

// TEST A -- genuine repository evidence remains VISIBLE, CITABLE, and PASSES.
// Existing behavior; must survive this change untouched.
func TestTypedVisibility_A_GenuineRepositoryEvidenceIsCitable(t *testing.T) {
	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(),
		snapshotWithTaskContext(currentReviewTaskID, ""), 7, designSHA, []string{realCite})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 0 {
		t.Fatalf("genuine repository evidence was rejected: %v", invalid)
	}
}

// TEST B -- THE INCIDENT, reproduced directly against the diagnostic: a
// model-invocation ref genuinely embedded in a bundle attached to the CURRENT
// task must not be told it was never shown. Today's describeInadmissibleReferences
// has no parameter through which to learn about a task's own attached
// evidence at all, so it always falls through to the generic message for
// this input -- which is what makes it wrong here. This is the RED case.
func TestTypedVisibility_B_EmbeddedExecutiveEvidenceIsVisibleButNoncitable(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("embedded executive evidence must remain non-citable (REJECT), got: %v", invalid)
	}

	message := describeInadmissibleReferences(context.Background(), sources, 7, invalid)
	if strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("diagnostic falsely denies visibility for a ref genuinely embedded in this task's own attached evidence: %q", message)
	}
	if !strings.Contains(message, "model-invocation:21") {
		t.Fatalf("diagnostic does not name the reference: %q", message)
	}
}

// TEST C -- a ref that is not part of the CURRENT task's evidence at all
// (never attached, never embedded anywhere relevant): NOT_VISIBLE, generic
// wording. Unaffected baseline.
func TestTypedVisibility_C_UnrelatedRefIsNotVisible(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a ref this execution never saw must still be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a ref genuinely absent from this execution must keep the honest generic wording: %q", message)
	}
}

// TEST D -- pure fabrication, not present anywhere relevant: NOT_VISIBLE,
// generic message. Unchanged baseline behavior.
func TestTypedVisibility_D_PureFabricationStaysGeneric(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:999999"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a fabricated model-invocation ref must be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("pure fabrication must keep the honest generic wording: %q", message)
	}
}

// TEST E -- a fabricated repository:// citation: NOT_VISIBLE, generic
// message. Unchanged baseline behavior (existing coverage, repinned here
// alongside its siblings for the full matrix).
func TestTypedVisibility_E_FabricatedRepositoryCitationStaysGeneric(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{inventedCi})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("an invented repository citation must be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a fabricated repository citation must keep the honest generic wording: %q", message)
	}
}

// TEST F -- a top-level canonical_document reference: VISIBLE, NONCITABLE,
// specific feedback naming its real class. This is PR#131's own guarantee;
// it must survive this change exactly as it was.
func TestTypedVisibility_F_CanonicalDocumentStaysVisibleButNoncitable(t *testing.T) {
	canonicalRef := "docs/canonical/capability-matrix.yaml"
	sources := snapshotWithTaskContext(currentReviewTaskID, canonicalRef)

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{canonicalRef})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a canonical document must remain non-citable: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, invalid)
	if strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a genuinely shown canonical document must not read as unseen: %q", message)
	}
	if !strings.Contains(message, "canonical/policy context") {
		t.Fatalf("diagnostic must still name the real class for a top-level source: %q", message)
	}
}

// TEST G -- THE NEGATIVE CONTROL: a token that merely resembles a real
// identifier, mentioned only in free prose, must never be classified as
// visible evidence identity by virtue of appearing as a substring somewhere.
// At BASE_SHA this already holds (nothing recognizes anything embedded at
// all yet); after the fix it must still hold, for the deliberate reason that
// the new classifier walks known typed fields only, never Content text.
func TestTypedVisibility_G_ArbitraryProseMentionIsNotStructurallyVisible(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a ref only mentioned in prose must still be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a ref with no structured backing must not be classified as visible: %q", message)
	}
}

// TEST H -- requirement_id=NULL echo: even once a reference is recognized as
// genuinely shown, VerifyEvidenceProvenance's decision does not change --
// authority is never inferred from visibility. A model-invocation ref that
// satisfies a requirement on its OWNING task is still rejected as evidence
// when offered by a DIFFERENT task, because repository_evidence remains the
// only citable class regardless of what any reference is authoritative for
// elsewhere.
func TestTypedVisibility_H_RequirementNullEchoGrantsNoAuthority(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("an embedded ref must still be rejected as citable evidence regardless of authority elsewhere: %v", invalid)
	}
}
