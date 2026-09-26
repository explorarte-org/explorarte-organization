package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// See design_host_governed.go: requirements the host enforces are not design content, and a revise
// must answer something the adversarial review raised.

func TestAReviseMustRestOnTheReview(t *testing.T) {
	base := DesignAdjudication{Verdict: AdjudicationRevise, RequiredChanges: []string{"Include the Finance guidance: at most 3 USD, at least 30 model calls."}}

	// Root 1382, round 1: every finding rejected, no evidence asked, two demands of its own.
	ownAgenda := base
	ownAgenda.RejectedFindings = []string{"AR-001"}
	err := AssertReviseRestsOnTheReview(ownAgenda)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a revise that answers no finding was accepted: %v", err)
	}
	for _, want := range []string{"rejects every finding", "return verdict freeze (or reject)", "budget and Finance guidance", "never required changes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %s", want, err)
		}
	}

	answersAFinding := base
	answersAFinding.AcceptedFindings = []string{"AR-001"}
	if err := AssertReviseRestsOnTheReview(answersAFinding); err != nil {
		t.Errorf("a revise that answers an accepted finding was refused: %v", err)
	}
	asksForEvidence := base
	asksForEvidence.EvidenceRequirements = []EvidenceRequirementProposal{{}}
	if err := AssertReviseRestsOnTheReview(asksForEvidence); err != nil {
		t.Errorf("a revise that asks for evidence was refused: %v", err)
	}
	for _, verdict := range []AdjudicationVerdict{AdjudicationFreeze, AdjudicationReject} {
		settled := DesignAdjudication{Verdict: verdict, RejectedFindings: []string{"AR-001"}}
		if err := AssertReviseRestsOnTheReview(settled); err != nil {
			t.Errorf("verdict %s with every finding rejected was refused: %v", verdict, err)
		}
	}
}

func TestReviewerAndAdjudicatorAreToldWhatTheHostEnforces(t *testing.T) {
	for _, want := range []string{
		"the budget and any guidance addressed to Finance",
		"how many departments take part",
		"which actor implements the change and when",
		"deployment or production access",
		"their absence is never a finding and never a required change",
		"is still a finding",
	} {
		if !strings.Contains(hostGovernedRequirementsConstraint, want) {
			t.Errorf("the host-governed rule lacks %q", want)
		}
	}
	if !strings.Contains(designAdjudicationPreamble, hostGovernedRequirementsConstraint) {
		t.Error("the adjudicator is not told which requirements the host enforces")
	}
	if !strings.Contains(designAdjudicationPreamble, "if you reject every finding, the design stands: return freeze (or reject), not revise") {
		t.Error("the adjudicator is not told that a revise must answer an accepted finding")
	}
	constraints := strings.Join(campaignTargetConstraints("add one case"), "\n")
	if !strings.Contains(constraints, hostGovernedRequirementsConstraint) {
		t.Error("the adversarial reviewer is not told which requirements the host enforces")
	}
	if !strings.Contains(constraints, "specifies for the change being designed") {
		t.Error("the reviewer is still told every requirement of the target must appear in the candidate")
	}
}

// On a campaign: the first adjudication rejects the only finding and sends the design back for a
// demand of its own; it is refused with feedback, the second attempt freezes, and the design settles in
// round 1 with no second round and no replan.
func TestAnAdjudicatorThatRejectsEveryFindingFreezesInsteadOfRevising(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	fixture.harness.adjudicationRewrite = func(task TaskRecord, body string) string {
		for _, attempt := range task.Attempts {
			if strings.Contains(attempt.ResultSummary, "rejects every finding") {
				return strings.NewReplacer(
					`"verdict":"revise"`, `"verdict":"freeze"`,
					`"accepted_findings":["AR-001"],"rejected_findings":[]`, `"accepted_findings":[],"rejected_findings":["AR-001"]`,
					`"required_changes":["Include the Finance guidance: at most 3 USD, at least 30 model calls, 20 subagents and depth 5."]`, `"required_changes":[]`,
				).Replace(body)
			}
		}
		return strings.Replace(body, `"accepted_findings":["AR-001"],"rejected_findings":[]`, `"accepted_findings":[],"rejected_findings":["AR-001"]`, 1)
	}
	fixture.harness.adjudicationRequiredChanges = []string{"Include the Finance guidance: at most 3 USD, at least 30 model calls, 20 subagents and depth 5."}

	// A refused attempt is the retryable path under test, not a failure of the drive.
	for i := 0; i < 24; i++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) && !errors.Is(err, ErrModelResultContractRejected) {
			t.Fatalf("resume %d: %v", i, err)
		}
		if run.State.Terminal() || run.State == StateBlocked || !designFreezePending(fixture.rootRecord(t)) {
			break
		}
	}

	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	adjudication, ok := findTaskByKey(all, childKey(fixture.root, "design-adjudication:round:1"))
	if !ok {
		t.Fatal("round 1 has no adjudication")
	}
	if len(adjudication.Attempts) != 2 || adjudication.Status != "completed" {
		t.Fatalf("adjudication attempts=%d status=%s, want the refused revise and the freeze", len(adjudication.Attempts), adjudication.Status)
	}
	if first := adjudication.Attempts[0]; first.State != "failed" || !strings.Contains(first.ResultSummary, "return verdict freeze (or reject)") {
		t.Fatalf("the own-agenda revise closed as %q with %q", first.State, first.ResultSummary)
	}
	if _, ok := findTaskByKey(all, childKey(fixture.root, "design-review:round:2")); ok {
		t.Fatal("a revise that answered no finding opened a second round")
	}
	if designFreezePending(fixture.rootRecord(t)) {
		t.Fatal("the design that survived its review did not freeze")
	}
}
