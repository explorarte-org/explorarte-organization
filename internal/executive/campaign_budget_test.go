package executive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// The invariant is negative -- the effective budget must NOT depend on which
// process submitted -- so the test that matters is the one that submits the
// same campaign from two differently configured orchestrators and compares.
// Asserting that one Submit records the right number would have passed before
// this fix too, because each process recorded its own number correctly.
func TestCampaignBudgetDoesNotDependOnTheSubmittingProcess(t *testing.T) {
	stated := DefaultCampaignBudget()
	stated.MaxUSD = modelpricing.USDFromDollars(17)
	stated.MaxTokens = 120_000_000

	request := SubmitRequest{
		Goal:           OwnerGoal{Goal: "rewrite the next steps", AcceptanceCriteria: []AcceptanceCriterion{{Text: "a plan exists", Phase: AcceptanceDesign}}},
		ActorRoleID:    OwnerRoleID,
		IdempotencyKey: "campaign-1",
		Budget:         &stated,
	}

	// Two runtimes that share nothing but the submission.
	first := newBudgetFixture(t)
	second := newBudgetFixture(t)
	if _, _, err := first.orchestrator.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.orchestrator.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if first.budgets.roots != 1 || second.budgets.roots != 1 {
		t.Fatalf("each submission must open exactly one campaign budget, got %d and %d", first.budgets.roots, second.budgets.roots)
	}
	if first.budgets.limits != second.budgets.limits {
		t.Fatalf("the campaign's ceilings differ by submitting process: %+v vs %+v", first.budgets.limits, second.budgets.limits)
	}
	if first.budgets.limits != stated {
		t.Fatalf("the recorded ceilings are not the ones the submission stated: %+v", first.budgets.limits)
	}
}

func TestAnUnstatedCampaignBudgetMeansTheDocumentedDefault(t *testing.T) {
	fixture := newBudgetFixture(t)
	request := SubmitRequest{
		Goal:           OwnerGoal{Goal: "rewrite the next steps", AcceptanceCriteria: []AcceptanceCriterion{{Text: "a plan exists", Phase: AcceptanceDesign}}},
		ActorRoleID:    OwnerRoleID,
		IdempotencyKey: "campaign-default",
	}
	if _, _, err := fixture.orchestrator.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if fixture.budgets.limits != DefaultCampaignBudget() {
		t.Fatalf("an unstated budget must mean the documented default, got %+v", fixture.budgets.limits)
	}
	// The point of a documented default rather than a configured one: it is
	// the same value in every process, so silence cannot diverge either.
	other := newBudgetFixture(t)
	if _, _, err := other.orchestrator.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if other.budgets.limits != fixture.budgets.limits {
		t.Fatal("an unstated budget resolved differently in a second process")
	}
}

// An unusable ceiling must stop the submission before anything durable exists.
// Creating the campaign first and failing to record its budget afterwards
// would leave a root running under whatever the ledger defaulted to, which is
// the shape of the defect being removed.
func TestAnInvalidCampaignBudgetIsRefusedBeforeTheCampaignExists(t *testing.T) {
	invalid := DefaultCampaignBudget()
	invalid.MaxTokens = 0
	fixture := newBudgetFixture(t)
	_, _, err := fixture.orchestrator.Submit(context.Background(), SubmitRequest{
		Goal:           OwnerGoal{Goal: "rewrite the next steps", AcceptanceCriteria: []AcceptanceCriterion{{Text: "a plan exists", Phase: AcceptanceDesign}}},
		ActorRoleID:    OwnerRoleID,
		IdempotencyKey: "campaign-invalid",
		Budget:         &invalid,
	})
	if err == nil {
		t.Fatal("a campaign cannot be created under a ceiling that authorizes nothing")
	}
	if !strings.Contains(err.Error(), "campaign budget") {
		t.Fatalf("the refusal must say what was wrong, got %v", err)
	}
	if len(fixture.tasks.tasks) != 0 {
		t.Fatalf("nothing durable may exist after a refused submission, found %d tasks", len(fixture.tasks.tasks))
	}
	if fixture.budgets.roots != 0 {
		t.Fatal("no campaign budget may have been opened")
	}
}

// ---- fixture ---------------------------------------------------------

type budgetFixture struct {
	orchestrator *Orchestrator
	tasks        *memoryTasks
	budgets      *recordingCampaignBudgets
}

func newBudgetFixture(t *testing.T) *budgetFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	budgets := &recordingCampaignBudgets{}
	ceo := RoleRef{ID: CEORoleID, Enabled: true, Executable: true, UnitID: "empresa"}
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: newMemoryAcceptance(),
		OrganizationID: "explorarte",
		Registry: fakeRegistry{
			rev:   RevisionRef{ID: 7},
			units: map[string]UnitRef{"empresa": {ID: "empresa", Operational: true, LeaderRoleID: ceo.ID}},
			roles: map[string]RoleRef{ceo.ID: ceo},
		},
		Tasks: tasksPort, Contexts: &fakeContexts{}, Assignments: fakeAssignments{},
		Principals: newFakePrincipals(), Models: &fakeModels{}, Harness: &fakeHarness{},
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}, WithAgentBudgets(budgets))
	if err != nil {
		t.Fatal(err)
	}
	return &budgetFixture{orchestrator: orchestrator, tasks: tasksPort, budgets: budgets}
}

// recordingCampaignBudgets keeps what the campaign was actually opened with.
// It holds no ceilings of its own, because the provider no longer can -- that
// field was the second representation this fix removed.
type recordingCampaignBudgets struct {
	roots  int
	limits CampaignBudget
}

func (b *recordingCampaignBudgets) CreateRootBudget(_ context.Context, _ TaskRecord, limits CampaignBudget, _ time.Time) error {
	b.roots++
	b.limits = limits
	return nil
}

func (b *recordingCampaignBudgets) InheritForChild(context.Context, TaskRecord, TaskRecord, int64, time.Time) error {
	return nil
}
