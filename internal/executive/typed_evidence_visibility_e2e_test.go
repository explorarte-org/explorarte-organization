package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TYPED-EVIDENCE-VISIBILITY-FIX-005, end to end.
//
// The wiring under test is the same driveTypedTask provenance boundary
// SELF-EVIDENCE-PROVENANCE-002 already exercises live, this time with a
// genuine executive-evidence bundle attached to the review task BEFORE it is
// dispatched -- exactly what EvidenceTasks.attachDepartmentBundle does in
// production (runtimeadapter/evidence_tasks.go), reproduced here via the same
// RecordEvidence port the real decorator calls, so the fixture proves the
// same fact production code proves: a bundle attached to task_evidence is
// visible to that task's own future executions without any additional
// plumbing.

// TEST I -- THE INCIDENT, end to end: a department review offers
// model-invocation:21 as evidence for an "accept" verdict, where the ONLY
// reason that ref is real is a bundle the host itself attached to this exact
// review task. Before this fix, VerifyEvidenceProvenance correctly rejects it
// (repository_evidence remains the only citable class) but the rejection
// falsely claims the host cannot verify it was shown -- it can, and did.
// After this fix, the rejection is truthful, and accept never becomes
// authoritative either way: CITABLE stays NO regardless of the diagnostic.
func TestTypedVisibility_I_DepartmentReviewEmbeddedRefIsRejectedTruthfully(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)

	// Drive until the review task exists, then attach the bundle to it --
	// the same durable fact EvidenceTasks writes in production, before the
	// task's own attempt ever starts.
	var reviewTask TaskRecord
	for i := 0; i < 10; i++ {
		if task, ok := departmentReviewTask(t, fixture); ok {
			reviewTask = task
			break
		}
		if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil &&
			!errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrModelResultContractRejected) {
			t.Fatalf("unexpected resume error while waiting for the review task: %v", err)
		}
	}
	if reviewTask.ID == 0 {
		t.Fatal("department review task never appeared")
	}
	if err := fixture.tasks.RecordEvidence(context.Background(), EvidenceCommand{
		TaskID: reviewTask.ID, Type: "result",
		Reference: "executive-evidence:department:ingenieria_ia:74ae8f9df6b6d17e",
		Metadata: map[string]any{"bundle": map[string]any{
			"schema_version": "executive-evidence.v1",
			"department_id":  "ingenieria_ia",
			"workers": []any{map[string]any{
				"task_id": float64(12432), "evidence_refs": []any{}, "task_evidence_refs": []any{"model-invocation:21"},
			}},
		}},
		Satisfies: false, RecordedBy: "executive-orchestrator",
	}); err != nil {
		t.Fatalf("attaching the executive-evidence bundle: %v", err)
	}

	fixture.harness.bodies[PurposeDepartmentReview] =
		`{"schema_version":"department-review/v2","verdict":"accept",` +
			`"findings":["design matches invocation 21"],"unsatisfied_criteria":[],` +
			`"evidence_refs":["model-invocation:21"],` +
			`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[]}`

	sawRejection := false
	for i := 0; i < 10; i++ {
		_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && errors.Is(err, ErrModelResultContractRejected) {
			sawRejection = true
			if strings.Contains(err.Error(), "cannot verify was shown") {
				t.Fatalf("a genuinely attached bundle ref must not be told it was never shown: %v", err)
			}
			if !strings.Contains(err.Error(), "model-invocation:21") {
				t.Fatalf("rejection does not name the offending ref: %v", err)
			}
		} else if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("unexpected resume error: %v", err)
		}
		task, ok := departmentReviewTask(t, fixture)
		if ok && task.Status == "failed" {
			break
		}
	}
	if !sawRejection {
		t.Fatal("a department review citing a bundle-embedded, non-repository reference was never rejected")
	}
}

// TEST J -- the honest, intended answer for a review with nothing citable:
// evidence_refs: [] alongside a normal accept verdict must still pass. This
// fix changes no acceptance policy -- an empty offer needed no provenance
// before and needs none now.
func TestTypedVisibility_J_DepartmentReviewEmptyEvidenceStillPasses(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentReview] =
		`{"schema_version":"department-review/v2","verdict":"accept",` +
			`"findings":["reviewed"],"unsatisfied_criteria":[],"evidence_refs":[],` +
			`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[]}`

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("an honest empty-evidence department review was rejected: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("an honest empty-evidence department review blocked the run: %+v", run)
	}
}
