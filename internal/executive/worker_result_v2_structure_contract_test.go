package executive

import (
	"errors"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-017-R17-V3 closed with task 34 (a zero-EvidenceRequirements
// PurposeDepartmentWorker) rejected three times over unstructured/invented
// evidence_refs. refsAreAllStructured (worker_result.go) enforces the
// worker-result/v2 evidence_refs-subset-of-evidence[] rule UNCONDITIONALLY --
// it never reads EvidenceRequirements -- but evidenceContractGuidance is
// correctly silent when required is empty (AUTONOMY-SMOKE-017-R8), and until
// this fix it was the ONLY place that stated the rule. A zero-requirement
// worker was therefore judged against a structural invariant its own
// ExecutionContract never mentioned, and guessed: a real canonical document
// with no evidence[] entry, then a self-citation to its own task, then a
// composed repository:// URI it had never been shown.
//
// These guards pin the repair: the v2 structural rule now reaches every
// PurposeDepartmentWorker execution unconditionally, independent of
// evidenceContractGuidance and of EvidenceRequirements.

// GUARD A -- the parser already accepts a fully empty, ungrounded v2 result.
// This was always true; it pins the shape the new guidance must point the
// worker toward.
func TestParserAcceptsEmptyV2EvidenceWhenNothingIsCited(t *testing.T) {
	limits := DefaultLimits()
	artifact := []byte(`{"schema_version":"worker-result/v2","summary":"Scoped the campaign constraints.","evidence_refs":[],"evidence":[]}`)
	if _, err := ParseWorkerResult(artifact, limits); err != nil {
		t.Fatalf("an empty evidence_refs/evidence pair must parse cleanly, got: %v", err)
	}
}

// GUARD B -- the parser already rejects an unstructured ref. This is task
// 34's second attempt, verbatim: a bare self-citation with nothing behind it.
func TestParserRejectsUnstructuredV2Ref(t *testing.T) {
	limits := DefaultLimits()
	artifact := []byte(`{"schema_version":"worker-result/v2","summary":"Scoped.","evidence_refs":["task:34"],"evidence":[]}`)
	_, err := ParseWorkerResult(artifact, limits)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an evidence_refs entry with no structured backing must stay a contract rejection: %v", err)
	}
	if !strings.Contains(err.Error(), "task:34") {
		t.Fatalf("rejection feedback must name the offending ref, got: %v", err)
	}
}

// GUARD C -- the parser already accepts a properly structured pair.
func TestParserAcceptsStructuredV2Pair(t *testing.T) {
	limits := DefaultLimits()
	artifact := []byte(`{"schema_version":"worker-result/v2","summary":"Scoped.",` +
		`"evidence_refs":["real-ref"],` +
		`"evidence":[{"claim":"c","subject":"s","relation":"context","ref":"real-ref"}]}`)
	if _, err := ParseWorkerResult(artifact, limits); err != nil {
		t.Fatalf("a properly structured ref must parse cleanly, got: %v", err)
	}
}

// GUARD D -- THE GAP ITSELF. Before the fix, a zero-requirement
// PurposeDepartmentWorker's ExecutionContract said nothing about the v2
// structural rule refsAreAllStructured was about to enforce, nor that an
// empty pair is the correct answer when nothing needs grounding. This is the
// RED regression: it fails against the pre-fix contract and must pass after.
func TestZeroRequirementDepartmentWorkerContractStatesTheV2StructuralRule(t *testing.T) {
	contract := executionContractFor(PurposeDepartmentWorker, nil)
	for _, want := range []string{
		"evidence_refs",
		"evidence[]",
		"evidence_refs: []",
		"evidence: []",
	} {
		if !strings.Contains(contract, want) {
			t.Fatalf("zero-requirement worker contract must state the v2 structural rule (missing %q):\n%s", want, contract)
		}
	}
	if !strings.Contains(contract, "invent") {
		t.Fatalf("zero-requirement worker contract must forbid inventing references:\n%s", contract)
	}
}

// GUARD E -- no regression: a worker with real EvidenceRequirements keeps
// receiving its existing, requirement-specific slot guidance unchanged.
// evidenceContractGuidance itself is not touched by this fix.
func TestNonZeroRequirementDepartmentWorkerContractStillStatesTheSlots(t *testing.T) {
	required := []EvidenceRequirement{{Subject: "MaxDesignRounds", Relations: []string{"definition"}}}
	contract := executionContractFor(PurposeDepartmentWorker, required)
	if !strings.Contains(contract, `subject="MaxDesignRounds", relation="definition"`) {
		t.Fatalf("non-zero-requirement worker contract lost its slot guidance:\n%s", contract)
	}
}

// GUARD F -- the new guidance is scoped to PurposeDepartmentWorker, the only
// purpose whose output schema ever offers worker-result/v2. It must not leak
// into purposes that never produce a WorkerResult.
func TestNonWorkerPurposesDoNotReceiveWorkerResultV2Guidance(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentPlan, PurposeDepartmentReview, PurposeDesignAdjudication} {
		contract := executionContractFor(purpose, nil)
		if strings.Contains(contract, "worker-result/v2") {
			t.Fatalf("%s must not receive worker-result/v2 structural guidance:\n%s", purpose, contract)
		}
	}
}
