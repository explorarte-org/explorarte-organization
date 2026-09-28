package executive

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// Local smoke #34 (root 1773): the round's review asked for a replan the host no longer grants, and
// the owner accepted the department's deliverable instead. The acceptance is a durable owner fact on
// the root naming that review; the review keeps its verdict; the run goes on to the adversarial
// review and adjudication with the work that exists, without buying the review again.
func TestTheOwnerCanAcceptADepartmentRoundTheHostDeclinedToReplan(t *testing.T) {
	fixture := newReplanFixture(t, 0, replanReviewNeedsReplan)
	fixture.drive(t)
	if got := fixture.rootRecord(t).ReasonCode; got != ReasonDepartmentReplansExhausted {
		t.Fatalf("scenario: reason_code=%q", got)
	}
	reviewsBefore := fixture.reviewInvocations()

	if _, err := fixture.orchestrator.AcceptDepartmentByOwner(context.Background(), fixture.root, "ingenieria_ia", CEORoleID); !errors.Is(err, ErrOwnerAcceptanceRefused) {
		t.Fatalf("the CEO accepted a department round: %v", err)
	}
	run, err := fixture.orchestrator.AcceptDepartmentByOwner(context.Background(), fixture.root, "ingenieria_ia", OwnerRoleID)
	if err != nil {
		t.Fatalf("owner acceptance: %v", err)
	}
	if run.State == StateBlocked {
		t.Fatalf("the run is still blocked after the owner's acceptance: %+v", run)
	}
	root := fixture.rootRecord(t)
	accepted := false
	for _, evidence := range root.Evidence {
		if strings.HasPrefix(evidence.Reference, "owner-accepts-department:ingenieria_ia:review:") {
			accepted = true
		}
	}
	if !accepted {
		t.Fatal("the owner's acceptance is not recorded on the root")
	}

	fixture.drive(t)
	purposes := fixture.purposes()
	if !slices.Contains(purposes, PurposeAdversarialReview) || !slices.Contains(purposes, PurposeDesignAdjudication) {
		t.Fatalf("the accepted round did not reach the adversarial review and the adjudication: %v", purposes)
	}
	if got := fixture.reviewInvocations(); got != reviewsBefore {
		t.Fatalf("the department review was bought %d more times after the owner accepted it", got-reviewsBefore)
	}
	for _, task := range fixture.tasks.tasks {
		if strings.Contains(task.IdempotencyKey, "followup-1") {
			t.Fatal("the declined replan's follow-up was materialized")
		}
	}
	if _, err := fixture.orchestrator.AcceptDepartmentByOwner(context.Background(), fixture.root, "ingenieria_ia", OwnerRoleID); !errors.Is(err, ErrOwnerAcceptanceRefused) {
		t.Fatalf("an acceptance of a run that is not blocked at the bound was not refused: %v", err)
	}
}

// Without the owner's acceptance the bound still holds, exactly as before.
func TestWithoutTheOwnersAcceptanceTheBoundStillHolds(t *testing.T) {
	fixture := newReplanFixture(t, 0, replanReviewNeedsReplan)
	fixture.drive(t)
	if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("resume without acceptance: %v", err)
	}
	if got := fixture.rootRecord(t).ReasonCode; got != ReasonDepartmentReplansExhausted {
		t.Fatalf("reason_code=%q", got)
	}
}
