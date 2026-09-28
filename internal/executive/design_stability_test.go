package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Root 1447 (smoke #18, 2026-09-27): the design worker spent two attempts on summaries of 4102 and 5013
// bytes, its deliverable restated the campaign's approval state (reviewers flagged it as an unsupported
// claim), and the second round's revise asked for evidence only a third round could supply.

func TestTheLongestObservedWorkerSummaryIsAccepted(t *testing.T) {
	limits := DefaultLimits()
	if limits.WorkerSummaryBytes() != 12000 || limits.MaxStringBytes != 4000 {
		t.Fatalf("summary limit %d / string limit %d, want 12000 / 4000", limits.WorkerSummaryBytes(), limits.MaxStringBytes)
	}
	// 10486 is the longest design summary refused at the former 8000 limit (smokes #32 to #39).
	for size, accepted := range map[int]bool{5013: true, 10486: true, 12000: true, 12001: false} {
		body := []byte(`{"schema_version":"worker-result/v1","summary":"` + strings.Repeat("a", size) + `","evidence_refs":[]}`)
		_, err := ParseWorkerResult(body, limits)
		if accepted && err != nil {
			t.Errorf("a %d-byte summary was refused: %v", size, err)
		}
		if !accepted && !errors.Is(err, ErrContractRejected) {
			t.Errorf("a %d-byte summary was accepted: %v", size, err)
		}
	}
	// Every other string keeps its own limit.
	longRef := []byte(`{"schema_version":"worker-result/v1","summary":"ok","evidence_refs":["` + strings.Repeat("a", 4001) + `"]}`)
	if _, err := ParseWorkerResult(longRef, limits); !errors.Is(err, ErrContractRejected) {
		t.Errorf("a 4001-byte evidence ref was accepted: %v", err)
	}
	// A full summary and its evidence still fit what the reviewers are shown of one deliverable.
	if candidateDeliverableBytes < limits.WorkerSummaryBytes()*2 {
		t.Errorf("a deliverable is shown %d bytes, too little for an %d-byte summary and its evidence", candidateDeliverableBytes, limits.WorkerSummaryBytes())
	}
}

func TestAThirdDesignRoundAnswersASecondRoundsEvidenceRequest(t *testing.T) {
	if got := DefaultLimits().MaxDesignRounds; got != 3 {
		t.Fatalf("MaxDesignRounds = %d, want 3", got)
	}
	fixture := newFreezeFixture(t, "revise", true)
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonDesignRoundsExhausted {
		t.Fatalf("an always-revising design is still bounded, got %+v", run)
	}
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findTaskByKey(all, childKey(fixture.root, "design-review:round:3")); !ok {
		t.Fatal("the third round never ran")
	}
	if _, ok := findTaskByKey(all, childKey(fixture.root, "design-review:round:4")); ok {
		t.Fatal("the loop ran past its bound")
	}
}

func TestTheCampaignsApprovalStateIsHostStateNotDesign(t *testing.T) {
	for _, want := range []string{"the campaign's approval and execution state", "that the campaign is approved for execution", "it is neither a finding nor evidence"} {
		if !strings.Contains(hostGovernedRequirementsConstraint, want) {
			t.Errorf("the reviewers are not told %q", want)
		}
	}
	if !strings.Contains(designDeliverableGuidance(), "Do not restate the campaign's approval or execution state") {
		t.Error("the design worker is not told to leave the approval state out of the design")
	}
}
