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
// how real the underlying document is. What it did not yet answer honestly
// is VISIBLE: describeInadmissibleReferences' "known" map was built only
// from SnapshotSource.Reference -- one identity per whole segment -- so a
// reference genuinely embedded INSIDE a segment's structured content (an
// executive-evidence bundle's own model-invocation:<id> entries, attached by
// the host to the reviewing task before dispatch) was indistinguishable from
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

// bundleEvidence builds the EvidenceRecord an executive-evidence bundle
// produces, matching the shape recordBundle (runtimeadapter/evidence_tasks.go)
// actually writes: Metadata["bundle"]["workers"][*]["task_evidence_refs"], a
// decoded map[string]any/[]any tree -- exactly what reading the real column
// back produces -- never a Content string a classifier would need to scan.
func bundleEvidence(taskEvidenceRefs ...string) EvidenceRecord {
	entries := make([]any, len(taskEvidenceRefs))
	for i, ref := range taskEvidenceRefs {
		entries[i] = ref
	}
	return EvidenceRecord{
		Reference:     "executive-evidence:department:ingenieria_ia:74ae8f9df6b6d17e",
		Type:          "result",
		RequirementID: 0, // NULL: the bundle row itself is never authoritative.
		Metadata: map[string]any{
			"bundle": map[string]any{
				"schema_version": "executive-evidence.v1",
				"department_id":  "ingenieria_ia",
				"workers": []any{
					map[string]any{
						"task_id":            float64(12432),
						"evidence_refs":      []any{},
						"task_evidence_refs": entries,
					},
				},
			},
		},
	}
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
// model-invocation ref embedded in a bundle genuinely attached to the CURRENT
// task must be recognized as shown, not told it was never seen.
func TestTypedVisibility_B_EmbeddedExecutiveEvidenceIsVisibleButNoncitable(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")
	evidence := []EvidenceRecord{bundleEvidence("model-invocation:21")}

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("embedded executive evidence must remain non-citable (REJECT), got: %v", invalid)
	}

	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("diagnostic falsely denies visibility for a genuinely shown embedded ref: %q", message)
	}
	if !strings.Contains(message, "model-invocation:21") {
		t.Fatalf("diagnostic does not name the reference: %q", message)
	}
}

// TEST C -- the same ref belongs to a DIFFERENT task's evidence, not the
// current task's: NOT_VISIBLE, generic wording. Visibility is scoped to the
// executing task, not to "does this identifier exist somewhere at all."
func TestTypedVisibility_C_RefFromAnotherTaskIsNotVisibleHere(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")
	// This task's OWN evidence carries a DIFFERENT embedded ref -- proving
	// the classifier reads this task's evidence content, not the name of
	// the ref being checked.
	evidence := []EvidenceRecord{bundleEvidence("model-invocation:99")}

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a ref this task's own evidence never carried must still be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a ref genuinely absent from this task's evidence must keep the honest generic wording: %q", message)
	}
}

// TEST D -- pure fabrication, not present anywhere relevant: NOT_VISIBLE,
// generic message. Unchanged baseline behavior.
func TestTypedVisibility_D_PureFabricationStaysGeneric(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")
	var evidence []EvidenceRecord

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:999999"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a fabricated model-invocation ref must be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("pure fabrication must keep the honest generic wording: %q", message)
	}
}

// TEST E -- a fabricated repository:// citation: NOT_VISIBLE, generic
// message. Unchanged baseline behavior (existing coverage, repinned here
// alongside its siblings for the full matrix).
func TestTypedVisibility_E_FabricatedRepositoryCitationStaysGeneric(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")
	var evidence []EvidenceRecord

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{inventedCi})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("an invented repository citation must be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
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
	var evidence []EvidenceRecord

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{canonicalRef})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a canonical document must remain non-citable: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a genuinely shown canonical document must not read as unseen: %q", message)
	}
	if !strings.Contains(message, "canonical/policy context") {
		t.Fatalf("diagnostic must still name the real class for a top-level source: %q", message)
	}
}

// TEST G -- THE NEGATIVE CONTROL: a token that merely resembles an embedded
// ref, present only in the bundle's free-text summary field (never in
// task_evidence_refs/evidence_refs, the two typed arrays the classifier is
// allowed to read), must NOT be recognized as visible evidence identity.
// This guards against strings.Contains(Content, ref)-shaped authority: the
// classifier must walk the bundle's KNOWN SCHEMA, never scan arbitrary text
// for a substring that happens to match.
func TestTypedVisibility_G_ArbitraryProseMentionIsNotStructurallyVisible(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")
	evidence := []EvidenceRecord{{
		Reference:     "executive-evidence:department:ingenieria_ia:deadbeefcafebabe",
		Type:          "result",
		RequirementID: 0,
		Metadata: map[string]any{
			"bundle": map[string]any{
				"schema_version": "executive-evidence.v1",
				"department_id":  "ingenieria_ia",
				"workers": []any{
					map[string]any{
						"task_id": float64(12432),
						// The mention lives in a free-text field, not in
						// evidence_refs/task_evidence_refs -- exactly the
						// shape a substring scan would wrongly catch and a
						// schema-aware walk correctly will not.
						"summary":            "See model-invocation:21 for the design rationale.",
						"evidence_refs":      []any{},
						"task_evidence_refs": []any{},
					},
				},
			},
		},
	}}

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a ref only mentioned in prose must still be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a ref present only as a prose mention, not in the typed evidence arrays, must NOT be classified as visible: %q", message)
	}
}

// TEST H -- requirement_id=NULL echo: a model-invocation ref that satisfies a
// requirement on its OWNING task carries requirement_id=NULL when echoed into
// the CURRENT task's bundle (exactly what recordBundle writes: Satisfies:
// false, unconditionally, for every bundle row). Recognizing the reference as
// shown does not make it citable or authoritative: VerifyEvidenceProvenance's
// decision is unaffected, and the diagnostic explains what it saw without
// claiming any grounding weight for it.
func TestTypedVisibility_H_RequirementNullEchoGrantsNoAuthority(t *testing.T) {
	sources := snapshotWithTaskContext(currentReviewTaskID, "")
	bundleRow := bundleEvidence("model-invocation:21")
	if bundleRow.RequirementID != 0 {
		t.Fatalf("a bundle row must be attached with requirement_id=NULL: %+v", bundleRow)
	}
	evidence := []EvidenceRecord{bundleRow}

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("an embedded ref with no requirement binding on the current task must still be rejected as evidence: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("the ref was genuinely shown; the diagnostic must say so even though it grants no authority: %q", message)
	}
}

// TEST -- execution scoping: an embedded ref inside a bundle attached to the
// current task must NOT be treated as visible if the task_context segment
// carrying it was itself dropped from this snapshot (Included: false). A
// bundle recorded in task_evidence is not the same fact as a bundle actually
// rendered to THIS invocation.
func TestTypedVisibility_EmbeddedRefRequiresItsOwnSegmentToHaveSurvivedAssembly(t *testing.T) {
	sources := stubSnapshotSources{sources: []SnapshotSource{
		{Kind: "task_context", Reference: taskRef(currentReviewTaskID), Version: "task.v1:1:x", Included: false},
	}}
	evidence := []EvidenceRecord{bundleEvidence("model-invocation:21")}

	orchestrator := &Orchestrator{}
	invalid, err := orchestrator.VerifyEvidenceProvenance(context.Background(), sources, 7, designSHA, []string{"model-invocation:21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 {
		t.Fatalf("a ref must still be rejected: %v", invalid)
	}
	message := describeInadmissibleReferences(context.Background(), sources, 7, currentReviewTaskID, evidence, invalid)
	if !strings.Contains(message, "cannot verify was shown") {
		t.Fatalf("a bundle whose own segment was dropped from assembly must not read as shown: %q", message)
	}
}
