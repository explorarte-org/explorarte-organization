package executive

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Root 1203 (2026-09-23): the design workers wrote summaries that said a case had been
// designed without stating it, and the adversarial reviewer and adjudicator -- whose
// bundle carried only the design phase's abstract acceptance criteria -- never saw the values
// the campaign named. The adjudicator invented its own example and both rounds were sent back
// for the same reason until the rounds ran out.
//
// B: the reviewer and the adjudicator judge the candidate against the owner's own target.
// A: the worker is told its summary IS the design.

const freezeGoal = "M2.1 -- design first, review adversarially, then freeze."

func TestTheReviewerAndTheAdjudicatorReceiveTheCampaignTarget(t *testing.T) {
	fixture := newFreezeFixture(t, "revise", true)
	fixture.drive(t)
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, round := range []string{"1", "2"} {
		for _, key := range []string{"design-review:round:" + round, "design-adjudication:round:" + round} {
			task, ok := findTaskByKey(all, childKey(fixture.root, key))
			if !ok {
				t.Fatalf("no task %s", key)
			}
			if !strings.Contains(task.Instructions, `"campaign_target":"`+freezeGoal+`"`) {
				t.Errorf("%s does not carry the owner's target:\n%s", key, task.Instructions)
			}
			for _, rule := range []string{
				"Judge the candidate design against it",
				"Do not invent names, values or examples of your own",
				"is a request, not evidence about the repository",
			} {
				if !strings.Contains(task.Instructions, rule) {
					t.Errorf("%s lacks the instruction %q", key, rule)
				}
			}
		}
		adjudication, _ := findTaskByKey(all, childKey(fixture.root, "design-adjudication:round:"+round))
		if !strings.Contains(adjudication.Instructions, "campaign_target is what the owner asked for") {
			t.Errorf("the adjudicator is not told how to use the target in round %s", round)
		}
	}
}

func TestTheTargetIsTheGoalWithoutTheHostsApprovedStateBlock(t *testing.T) {
	root := TaskRecord{Instructions: WrapHostCampaignState("Campaign state: APPROVED_FOR_EXECUTION.") + "\n\n" + `add a case named "digits adjacent to letters"`}
	got, err := campaignTargetFor(root, nil, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if got != `add a case named "digits adjacent to letters"` {
		t.Fatalf("target = %q", got)
	}
	if strings.Contains(got, "APPROVED_FOR_EXECUTION") || strings.Contains(got, HostCampaignStateBegin) {
		t.Fatalf("the host's own block reached the reviewer: %q", got)
	}
	if empty, err := campaignTargetFor(TaskRecord{Instructions: "  "}, nil, 16000); err != nil || empty != "" {
		t.Fatalf("an empty goal must give no target and no error: %q %v", empty, err)
	}
}

func TestATargetOverTheBudgetIsCutAndSaysSo(t *testing.T) {
	long := "name \"digits adjacent to letters\" " + strings.Repeat("x", 500)
	got, err := campaignTargetFor(TaskRecord{Instructions: long}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "name \"digits adjacent to letters\"") {
		t.Fatalf("the values the reviewer needs are at the start and must survive the cut: %q", got)
	}
	if !strings.Contains(got, "[campaign target cut at 100 bytes]") {
		t.Fatalf("a cut target must say it was cut: %q", got)
	}
	if len(got) > 100+len("\n[campaign target cut at 100 bytes]") {
		t.Fatalf("the target is not bounded: %d bytes", len(got))
	}
}

// The target goes to a reviewer whose context admits public and sanitized data. It is held to
// the rule the candidate is held to: an owner's request that reproduces organizational source is
// refused, not trimmed.
func TestATargetThatReproducesOrganizationalSourceIsRefused(t *testing.T) {
	source := "func ExtractDigitRuns(text string) []string { return digitRun.FindAllString(text, -1) } // the third retrieval channel"
	organizational := []OrganizationalSource{{Reference: "repository://x/identifiers.go", Content: source}}
	_, err := campaignTargetFor(TaskRecord{Instructions: "Please change this: " + source}, organizational, 16000)
	if err == nil {
		t.Fatal("a target that reproduces organizational source was allowed to reach the reviewer")
	}
	if !strings.Contains(err.Error(), "campaign target") {
		t.Fatalf("the refusal does not say what was refused: %v", err)
	}
	// A target that merely NAMES symbols and paths is allowed: those always cross.
	if _, err := campaignTargetFor(TaskRecord{Instructions: "Add one case to TestExtractDigitRunsCoreCases in internal/identifiers/identifiers_test.go"}, organizational, 16000); err != nil {
		t.Fatalf("a target naming a symbol and a path was refused: %v", err)
	}
}

func TestTheTargetConstraintsAreOnlyStatedWhenThereIsATarget(t *testing.T) {
	if got := campaignTargetConstraints(""); len(got) != 0 {
		t.Fatalf("no target, yet the reviewer is told how to use one: %v", got)
	}
	got := campaignTargetConstraints("t")
	if len(got) != 5 {
		t.Fatalf("got %d constraints, want the five that say how to use a target", len(got))
	}
	if !slices.Contains(got, hostGovernedRequirementsConstraint) {
		t.Fatal("the reviewer is not told which target requirements the host enforces")
	}
}

// A: the deliverable rule reaches department workers -- the producers whose summary becomes the
// candidate design -- and nobody else.
func TestOnlyDepartmentWorkersAreToldTheDeliverableRule(t *testing.T) {
	guidance := designDeliverableGuidance()
	for _, want := range []string{
		"Your summary IS the design",
		"State the proposal itself in concrete terms",
		"Do not substitute values, names or examples of your own",
		"never from repository text you were shown",
		"Do not claim the change was made",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("the deliverable rule lacks %q", want)
		}
	}
	if !strings.Contains(executionContractFor(PurposeDepartmentWorker, nil), guidance) {
		t.Error("a department worker is not told the deliverable rule")
	}
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentPlan, PurposeDepartmentReview, PurposeDesignAdjudication, PurposeAdversarialReview, PurposeCEOPlan, PurposeCEOClosure} {
		if strings.Contains(executionContractFor(purpose, nil), "Your summary IS the design") {
			t.Errorf("%s is told a rule that is a worker's", purpose)
		}
	}
}
