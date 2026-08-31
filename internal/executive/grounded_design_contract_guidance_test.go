package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// GROUNDED-DESIGN-CONTRACT-GAP-003.
//
// AUTONOMY-SMOKE-017-R17-V4 died one stage later than v3, at task 11992
// ("Design grounded stage graph"): a zero-EvidenceRequirements
// PurposeDepartmentWorker whose snapshot carried real canonical_document and
// role_profile context but no repository_evidence at all. All three attempts
// cited real canonical/policy paths as evidence_refs -- accurate about their
// content, wrong about their admissibility -- because nothing had ever told
// the worker that organizational context is guidance to follow, not evidence
// to cite. Sibling task 11991 (repository-evidence collection) got this
// right only because ITS OWN instructions said so explicitly ("reject any
// evidence not actually shown"); nothing said it structurally. This is the
// same shape of gap workerResultV2StructureGuidance (PR#129) closed for the
// v2 document's STRUCTURE; this closes it for the document's admissible
// CONTENT CLASS.
//
// VerifyEvidenceProvenance (PR#130) already rejects this correctly -- 0/6
// citation attempts across R17-v3 and R17-v4 were ever falsely accepted.
// This fix does not touch that decision. It closes the gap upstream, in what
// the producer is told before it answers, and in what it is told after a
// rejection.

// GUARD A -- THE GAP ITSELF (Candidate A). Before the fix, a zero-requirement
// PurposeDepartmentWorker's ExecutionContract said nothing distinguishing
// organizational context (canonical/policy documents, role and department
// profiles) from citable repository evidence. This is the RED regression: it
// fails against the pre-fix contract and must pass after.
func TestZeroRequirementDepartmentWorkerContractDistinguishesCanonicalContextFromEvidence(t *testing.T) {
	contract := executionContractFor(PurposeDepartmentWorker, nil)
	for _, want := range []string{
		"not evidence to cite",
		"canonical or policy documents",
		"organizational context instead",
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("zero-requirement worker contract must distinguish organizational context from citable repository evidence (missing %q):\n%s", want, contract)
		}
	}
}

// GUARD B -- no regression: the new guidance must not leak into purposes
// that never produce a WorkerResult, mirroring PR#129's own boundary for
// workerResultV2StructureGuidance.
func TestNonWorkerPurposesDoNotReceiveTheCanonicalVsEvidenceGuidance(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentPlan, PurposeDepartmentReview, PurposeDesignAdjudication} {
		contract := executionContractFor(purpose, nil)
		if strings.Contains(contract, "not evidence to cite") {
			t.Fatalf("%s must not receive the canonical-vs-evidence guidance:\n%s", purpose, contract)
		}
	}
}

// GUARD C -- THE GAP ITSELF (Candidate B). Before the fix, a provenance
// rejection for a reference that traces to a REAL, present-in-context,
// non-repository_evidence source (task 11992's canonical/policy citations)
// carried the same generic "cannot verify was shown" text as a pure
// fabrication. Task 11992's second attempt responded to that generic
// message by CITING MORE, not less -- the feedback never told it the
// reference was real, only that it was inadmissible, which reads exactly
// like "try a different citation" instead of "stop citing this class of
// thing at all". This is the RED regression: the message must name the
// source's actual kind after the fix, and must NOT before it.
func TestProvenanceRejectionNamesCanonicalContextSpecifically(t *testing.T) {
	sources := stubSnapshotSources{sources: []SnapshotSource{
		{Kind: "canonical_document", Reference: "docs/canonical/capability-matrix.yaml", Version: "sha256:abc123", Included: true},
	}}
	orchestrator := &Orchestrator{snapshotSources: sources}
	err := orchestrator.verifyOfferedEvidenceProvenance(context.Background(), 7, designSHA,
		[]string{"docs/canonical/capability-matrix.yaml"})
	if err == nil || !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a real but non-repository-evidence reference must still be rejected, got: %v", err)
	}
	if !strings.Contains(err.Error(), "docs/canonical/capability-matrix.yaml") {
		t.Fatalf("rejection feedback must still name the offending ref, got: %v", err)
	}
	if !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("rejection feedback must name canonical/policy context specifically instead of the generic message, got: %v", err)
	}
	if strings.Contains(err.Error(), "which the host cannot verify was shown") {
		t.Fatalf("a reference the host DOES recognize (as the wrong class) must not be told it could not be verified at all, got: %v", err)
	}
}

// GUARD D -- a true fabrication (nothing in context at all, of any kind)
// must keep the original, honest "cannot verify was shown" wording. The fix
// must never claim to know what a reference is when it traces to nothing.
func TestProvenanceRejectionKeepsGenericWordingForTrueFabrications(t *testing.T) {
	orchestrator := &Orchestrator{snapshotSources: snapshotWith()}
	err := orchestrator.verifyOfferedEvidenceProvenance(context.Background(), 7, designSHA, []string{"task:34"})
	if err == nil || !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a bare self-citation must still be rejected, got: %v", err)
	}
	if !strings.Contains(err.Error(), "task:34") {
		t.Fatalf("rejection feedback must name the offending ref, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot verify was shown") {
		t.Fatalf("a true fabrication must keep the honest generic wording, got: %v", err)
	}
}
