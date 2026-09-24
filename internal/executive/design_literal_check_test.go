package executive

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// See design_literal_check.go: a design worker's summary must state every identifier its own
// acceptance criteria name. These pin what counts as one (narrowly, so a correct worker is never
// blocked), where the check applies, and the whole loop on a real campaign.

func TestIdentifiersNamedByIsNarrow(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"the root-1265 name", "State that the edit adds one case to the table of TestExtractDigitRunsCoreCases in a file.",
			[]string{"TestExtractDigitRunsCoreCases"}},
		{"several, in order, without duplicates", "Use MaxDesignRounds and ErrContractRejected; MaxDesignRounds again.",
			[]string{"MaxDesignRounds", "ErrContractRejected"}},
		{"a go test name with an underscore", "Run TestFoo_BarBaz for it.", []string{"TestFoo_BarBaz"}},
		{"plain prose", "State that the proposed edit adds exactly one new case and changes no other line.", nil},
		{"one-hump product names", "Use GitHub, PostgreSQL, TypeScript, OpenTelemetry and YouTube.", nil},
		{"acronyms and ALL_CAPS constants", "See README, JSON, HTTPS and MAX_DESIGN_ROUNDS.", nil},
		{"paths and commands", "Touch internal/identifiers/identifiers_test.go and run go test ./internal/identifiers/...", nil},
		{"numbers, commit ids and mixed literals", "Input abc123def45 at 38bee24 with 30 calls and 3 USD.", nil},
		{"too short to be a name", "Call aBcDe now.", nil},
		{"a lower-case snake key", "Task design_digit_runs_test_case owns it.", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := identifiersNamedBy([]string{testCase.text}); !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("identifiersNamedBy(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
		})
	}
}

func TestOmittedIdentifiersReportsExactlyTheMissingOnes(t *testing.T) {
	criteria := []string{"State the table of TestExtractDigitRunsCoreCases.", "Keep MaxDesignRounds unchanged."}
	if got := omittedIdentifiers("Adds one case to the test table; MaxDesignRounds is unchanged.", criteria); !reflect.DeepEqual(got, []string{"TestExtractDigitRunsCoreCases"}) {
		t.Errorf("omitted = %v", got)
	}
	if got := omittedIdentifiers("Adds a case to TestExtractDigitRunsCoreCases; MaxDesignRounds stays.", criteria); got != nil {
		t.Errorf("a summary that states both was refused: %v", got)
	}
	if got := omittedIdentifiers("anything", nil); got != nil {
		t.Errorf("no criteria, nothing to omit: %v", got)
	}
}

// The check governs only a design whose freeze is pending.
func TestTheCheckAppliesOnlyWhileADesignFreezeIsPending(t *testing.T) {
	task := TaskRecord{AcceptanceCriteria: []string{"State TestExtractDigitRunsCoreCases."}}
	pending := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "pending"}}}
	satisfied := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "satisfied"}}}

	if err := verifyDesignNamesTheCriteriaIdentifiers(pending, task, "the test table"); !errors.Is(err, ErrContractRejected) ||
		!strings.Contains(err.Error(), "TestExtractDigitRunsCoreCases") {
		t.Errorf("a pending freeze did not refuse the omission: %v", err)
	}
	if err := verifyDesignNamesTheCriteriaIdentifiers(pending, task, "TestExtractDigitRunsCoreCases gets one case"); err != nil {
		t.Errorf("a complete summary was refused: %v", err)
	}
	if err := verifyDesignNamesTheCriteriaIdentifiers(TaskRecord{}, task, "the test table"); err != nil {
		t.Errorf("a run with no design freeze was checked: %v", err)
	}
	if err := verifyDesignNamesTheCriteriaIdentifiers(satisfied, task, "the test table"); err != nil {
		t.Errorf("a satisfied freeze was checked: %v", err)
	}
}

func TestTheFeedbackIsBounded(t *testing.T) {
	criteria := ""
	for _, name := range []string{"AaAaAaAa", "BbBbBbBb", "CcCcCcCc", "DdDdDdDd", "EeEeEeEe", "FfFfFfFf", "GgGgGgGg", "HhHhHhHh", "IiIiIiIi", "JjJjJjJj"} {
		criteria += name + " "
	}
	pending := TaskRecord{Requirements: []RequirementRecord{{Key: designfreeze.RequirementKey, Status: "pending"}}}
	err := verifyDesignNamesTheCriteriaIdentifiers(pending, TaskRecord{AcceptanceCriteria: []string{criteria}}, "nothing")
	if err == nil || strings.Contains(err.Error(), "IiIiIiIi") {
		t.Errorf("the feedback was not capped at %d names: %v", maxOmittedReported, err)
	}
}

// The whole loop on a campaign: the first attempt omits the name and is refused with feedback
// naming it; the retry reads that feedback, states it, and the run reaches the freeze -- inside
// one design round, with no reviewer or adjudicator spent on the omission.
func TestAWorkerThatOmitsACriteriaIdentifierIsCorrectedInsideTheRound(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	fixture.harness.bodies[PurposeDepartmentPlan] =
		`{"schema_version":"department-plan/v1","department_id":"ingenieria_ia",` +
			`"tasks":[{"client_key":"design-1","assigned_role_id":"ingenieria_ia/qa","task_class":"engineering.design",` +
			`"title":"Draft the candidate design","instructions":"Write the design.",` +
			`"acceptance_criteria":["State that the edit adds one case to the table of TestExtractDigitRunsCoreCases."],` +
			`"dependencies":[],"requirements":[],"priority":50}],` +
			`"review_criteria":["design is complete"],"unresolved":[]}`
	fixture.harness.departmentWorkerBody = func(task TaskRecord) string {
		summary := "Adds one case to the test table."
		for _, attempt := range task.Attempts {
			if strings.Contains(attempt.ResultSummary, "TestExtractDigitRunsCoreCases") {
				summary = "Adds one case to the table of TestExtractDigitRunsCoreCases."
			}
		}
		return `{"schema_version":"worker-result/v1","summary":"` + summary + `","evidence_refs":[]}`
	}

	driveCapability(t, fixture, 40)

	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	var worker TaskRecord
	for _, task := range all {
		if strings.Contains(task.IdempotencyKey, ":worker:ingenieria_ia:design-1") {
			worker = task
		}
	}
	if len(worker.Attempts) != 2 {
		t.Fatalf("worker attempts=%d, want the refused one and the corrected one", len(worker.Attempts))
	}
	if worker.Attempts[0].State != "failed" ||
		!strings.Contains(worker.Attempts[0].ResultSummary, "omits identifiers your acceptance criteria name: TestExtractDigitRunsCoreCases") {
		t.Fatalf("the omission was not refused with the missing name: %+v", worker.Attempts[0])
	}
	if worker.Status != "completed" {
		t.Fatalf("the corrected worker did not complete: %s", worker.Status)
	}
	for _, task := range all {
		if designRoundOf(task.IdempotencyKey) >= 2 {
			t.Fatalf("the omission cost a design round: %s", task.IdempotencyKey)
		}
	}
	if designFreezePending(fixture.rootRecord(t)) {
		t.Fatal("the corrected design never reached the freeze")
	}
}
