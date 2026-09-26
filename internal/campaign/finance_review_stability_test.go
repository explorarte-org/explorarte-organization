package campaign_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
)

// Proposals 34 and 35 (2026-09-26) were the same campaign under two titles: one review recommended it,
// the next returned changes_requested with corrections that asked for nothing, and only "recommended"
// can be approved. See financeReviewTemperature.

func runFinanceReviewCapturingConfig(t *testing.T) modelruntimeadapter.Config {
	t.Helper()
	store, taskCoord, _, _ := setupDeterministicFixture(t)
	prop := createTestProposal(t, store, "org-test", "Stability Campaign", "Goal")
	model := &capturingModel{script: func(executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
		return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: validFinanceOutputJSON, InvocationRef: "stability-inv-1"}, nil
	}}
	var captured []modelruntimeadapter.Config
	finSvc, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID: "org-test",
		Requirements:   permissiveRequirements(),
		Store:          store,
		Tasks:          taskCoord,
		Authorizer: fakeAuthorizer{grants: map[string]bool{
			"empresa/ceo:campaign.financial_review.request":                      true,
			"empresa/ceo:campaign.financial_review.read":                         true,
			"negocio/administrador_financiero:campaign.financial_review.perform": true,
		}},
		Authority:         passAuthority{},
		HarnessHistory:    executionharness.NewMemoryHistoryStore(),
		DescriptorStore:   executionharness.NewMemoryRunDescriptorStore(),
		HolderPrincipalID: "finance-principal-stability-test",
		ContextBuilder:    &fakeFinanceContextBuilder{taskCoord: taskCoord},
		NewModelExecutor: func(config modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			captured = append(captured, config)
			return model, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, task, _, err := finSvc.RequestReview(context.Background(), campaign.RequestReviewParams{
		OrganizationID: "org-test", ProposalID: prop.ID, RequestedByRoleID: "empresa/ceo", RequestedFromTaskID: 20, ToolCallID: "call_stability",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = finSvc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: task.ID, ReviewRequestID: req.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Fatalf("model executors built = %d, want 1", len(captured))
	}
	return captured[0]
}

func TestEveryFinanceReviewIsSampledGreedily(t *testing.T) {
	config := runFinanceReviewCapturingConfig(t)
	if config.Temperature == nil {
		t.Fatal("the Finance review is sampled at the provider's default temperature")
	}
	if *config.Temperature != 0 {
		t.Fatalf("the Finance review is sampled at temperature %v, want 0", *config.Temperature)
	}
}

func TestTheFinanceContractDoesNotTurnWhatIsNeverObservedOrChosenIntoAVerdict(t *testing.T) {
	text := runFinanceReviewCapturingConfig(t).ExecutionContractInstructions
	for _, want := range []string{
		// Treasury is never observed: listing it is required, refusing on it is not.
		"Always list them in \"missing_information\"",
		"on its own it is never a reason for a verdict other than \"recommended\"",
		"Use \"insufficient_data\" only when the proposal itself depends on cash, revenue or runway",
		// A correction is something the proposal must change, not a ceiling Finance can choose.
		"\"changes_requested\" means the PROPOSAL must change",
		"Choosing recommended_budget is your job, not a correction",
		"exceeding the floor is never a defect and never a correction",
		"budget source OWNER_LIMIT",
		// The floor is still stated and still binding.
		"HOST EXECUTION BUDGET FLOOR (TRUSTED HOST FACTS)",
		"\"max_wall_time_ms\": <integer >= ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Finance contract lacks %q", want)
		}
	}
	// Review 31 recommended exactly the floor's wall time: the example must show no number to copy.
	if copyable := regexp.MustCompile(`"max_[a-z_]+": [0-9]`).FindAllString(text, -1); len(copyable) > 0 {
		t.Errorf("the example still shows copyable budget values: %v", copyable)
	}
	if strings.Contains(text, "produce verdict \"insufficient_data\" or clearly qualify") {
		t.Error("the contract still tells Finance to refuse on unobserved treasury")
	}
}
