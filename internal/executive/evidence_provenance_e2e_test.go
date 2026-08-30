package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// SELF-EVIDENCE-PROVENANCE-002, end to end.
//
// The wiring under test is driveTypedTask's provenance check reaching a live
// campaign exactly as AUTONOMY-SMOKE-017-R17-V3's task 34 reached it: a
// repository-grounded campaign whose round carries zero
// EvidenceRequirements. ValidateEvidenceSupply and ValidateEvidenceStructure
// both no-op at required==0, so before this fix nothing checked a
// voluntarily offered evidence[] item -- or a department review's
// evidence_refs -- against the world at all.

// departmentReviewTask finds the campaign's department review task, the
// review-side analogue of designWorkerTask (evidence_wiring_e2e_test.go).
func departmentReviewTask(t *testing.T, f *wiringFixture) (TaskRecord, bool) {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if task.TaskClass == TaskClassCoordinationDeptReview {
			return task, true
		}
	}
	return TaskRecord{}, false
}

// GUARD -- THE ATTACK, reproduced live: a zero-requirement worker offers its
// own task reference as evidence. Before this fix it reached completion
// silently (refsAreAllStructured accepted it, nothing else looked); after,
// it is a contract rejection like any other invented reference.
func TestAZeroRequirementWorkerCannotOfferItsOwnTaskAsEvidence(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Scoped the campaign constraints.",` +
			`"evidence_refs":["task:34"],` +
			`"evidence":[{"claim":"this execution concerns task 34","subject":"task","relation":"context","ref":"task:34"}]}`

	sawRejection := false
	for i := 0; i < 10; i++ {
		_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && errors.Is(err, ErrModelResultContractRejected) {
			sawRejection = true
			if !strings.Contains(err.Error(), "task:34") {
				t.Fatalf("rejection does not name the offending ref: %v", err)
			}
		} else if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("unexpected resume error: %v", err)
		}
		task, ok := designWorkerTask(t, fixture)
		if ok && task.Status == "failed" {
			break
		}
	}
	if !sawRejection {
		t.Fatal("a self-citation offered as evidence with zero requirements was never rejected")
	}
}

// GUARD -- positive control: the same zero-requirement worker MAY cite
// something it was genuinely shown, even though nothing required it. A
// provenance boundary that rejected this would be indistinguishable from
// forbidding voluntary evidence outright, which is not the invariant.
func TestAZeroRequirementWorkerMayOfferAGenuinelyShownCitation(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Scoped the campaign constraints.",` +
			`"evidence_refs":["` + wiringDefRef + `"],` +
			`"evidence":[{"claim":"named for context","subject":"MaxDesignRounds","relation":"context","ref":"` + wiringDefRef + `"}]}`

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("a genuinely shown voluntary citation was rejected: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("a genuinely shown voluntary citation blocked the run: %+v", run)
	}
}

// GUARD -- the honest, intended answer for a zero-requirement worker with
// nothing to cite must still pass cleanly. This is
// fix/worker-result-v2-structural-contract's own guarantee, reconfirmed here
// because the provenance check now rides alongside it on the same path.
func TestAZeroRequirementWorkerMayStillOfferNoEvidenceAtAll(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Scoped the campaign constraints.",` +
			`"evidence_refs":[],"evidence":[]}`

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("an honest empty-evidence answer was rejected: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("an honest empty-evidence answer blocked the run: %+v", run)
	}
}

// GUARD -- a fabricated repository:// URI is rejected the same way a bare
// self-citation is, proving the provenance boundary is a "was this really
// shown" question and not a per-namespace blacklist.
func TestAZeroRequirementWorkerCannotInventARepositoryCitation(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Scoped the campaign constraints.",` +
			`"evidence_refs":["` + wiringBogus + `"],` +
			`"evidence":[{"claim":"invented","subject":"task","relation":"context","ref":"` + wiringBogus + `"}]}`

	sawRejection := false
	for i := 0; i < 10; i++ {
		_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && errors.Is(err, ErrModelResultContractRejected) {
			sawRejection = true
		} else if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("unexpected resume error: %v", err)
		}
		task, ok := designWorkerTask(t, fixture)
		if ok && task.Status == "failed" {
			break
		}
	}
	if !sawRejection {
		t.Fatal("an invented repository citation offered as evidence with zero requirements was never rejected")
	}
}

// GUARD -- a department review (v2) cannot convert a fabricated self-citation
// into supporting evidence either. Verdict is what actually gates root
// completion (validateRunCompletionEvidence requires ReviewAccept); this
// pins that a fabricated ref is refused as a contract violation before
// Accept is ever treated as authoritative -- the property that motivated
// this half of the fix.
func TestADepartmentReviewV2CannotOfferItsOwnTaskAsEvidence(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentReview] =
		`{"schema_version":"department-review/v2","verdict":"accept",` +
			`"findings":["reviewed"],"unsatisfied_criteria":[],"evidence_refs":["task:1:context"],` +
			`"proposed_followup_tasks":[],"followup_ownership":[],"revision_outcomes":[]}`

	sawRejection := false
	for i := 0; i < 10; i++ {
		_, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && errors.Is(err, ErrModelResultContractRejected) {
			sawRejection = true
			if !strings.Contains(err.Error(), "task:1:context") {
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
		t.Fatal("a department review's self-citation was never rejected")
	}
}

// GUARD -- a department review (v1) is untouched: provenance enforcement is
// scoped to v2, mirroring fix/worker-result-v2-structural-contract's own
// v1/v2 boundary. v1 predates the structured-evidence contract entirely and
// carries no established provenance semantics to enforce.
func TestADepartmentReviewV1IsNotSubjectToProvenanceChecking(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentReview] =
		`{"schema_version":"department-review/v1","verdict":"accept",` +
			`"findings":["reviewed"],"unsatisfied_criteria":[],"evidence_refs":["task:1:context"],` +
			`"proposed_followup_tasks":[]}`

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("a v1 review was rejected on a field this fix deliberately does not police: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("a v1 review blocked the run: %+v", run)
	}
}
