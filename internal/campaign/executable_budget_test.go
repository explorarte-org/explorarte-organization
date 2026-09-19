package campaign

import (
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// permissiveTestRequirements is a floor every fixture budget clears: it lets
// tests that are not about feasibility exercise the rest of the contract.
func permissiveTestRequirements() ExecutionBudgetRequirements {
	return ExecutionBudgetRequirements{MinUSD: 1, MinTokens: 1, MinModelCalls: 1, MinWallTimeMS: 1, MinDepth: 1, MinRetries: 1, MinSubagents: 1}
}

func validTestBudget() BudgetRecommendation {
	return BudgetRecommendation{
		MaxUSD:        1.0,
		MaxTokens:     1000,
		MaxModelCalls: 2,
		MaxWallTimeMS: 60000,
		MaxDepth:      2,
		MaxRetries:    1,
		MaxSubagents:  1,
	}
}

// TestValidateExecutableBudget_AllSevenZeroDimensions table-tests that independently
// setting each of the seven dimensions to zero fails ValidateExecutableBudget and returns
// ErrInvalidExecutionBudget (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 6).
func TestValidateExecutableBudget_AllSevenZeroDimensions(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*BudgetRecommendation)
	}{
		{name: "MaxUSD_zero", modify: func(b *BudgetRecommendation) { b.MaxUSD = 0 }},
		{name: "MaxTokens_zero", modify: func(b *BudgetRecommendation) { b.MaxTokens = 0 }},
		{name: "MaxModelCalls_zero", modify: func(b *BudgetRecommendation) { b.MaxModelCalls = 0 }},
		{name: "MaxWallTimeMS_zero", modify: func(b *BudgetRecommendation) { b.MaxWallTimeMS = 0 }},
		{name: "MaxDepth_zero", modify: func(b *BudgetRecommendation) { b.MaxDepth = 0 }},
		{name: "MaxRetries_zero", modify: func(b *BudgetRecommendation) { b.MaxRetries = 0 }},
		{name: "MaxSubagents_zero", modify: func(b *BudgetRecommendation) { b.MaxSubagents = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := validTestBudget()
			tt.modify(&b)

			err := ValidateExecutableBudget(b)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !errors.Is(err, ErrInvalidExecutionBudget) {
				t.Fatalf("expected ErrInvalidExecutionBudget, got: %v", err)
			}
			if !errors.Is(err, agentbudget.ErrInvalidRequest) {
				t.Fatalf("expected agentbudget.ErrInvalidRequest, got: %v", err)
			}
		})
	}
}

// TestValidateExecutableBudget_NegativeDimensions table-tests all representable
// negative dimensions (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 7).
func TestValidateExecutableBudget_NegativeDimensions(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*BudgetRecommendation)
	}{
		{name: "MaxUSD_negative", modify: func(b *BudgetRecommendation) { b.MaxUSD = -1.0 }},
		{name: "MaxTokens_negative", modify: func(b *BudgetRecommendation) { b.MaxTokens = -1 }},
		{name: "MaxModelCalls_negative", modify: func(b *BudgetRecommendation) { b.MaxModelCalls = -1 }},
		{name: "MaxWallTimeMS_negative", modify: func(b *BudgetRecommendation) { b.MaxWallTimeMS = -1 }},
		{name: "MaxDepth_negative", modify: func(b *BudgetRecommendation) { b.MaxDepth = -1 }},
		{name: "MaxRetries_negative", modify: func(b *BudgetRecommendation) { b.MaxRetries = -1 }},
		{name: "MaxSubagents_negative", modify: func(b *BudgetRecommendation) { b.MaxSubagents = -1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := validTestBudget()
			tt.modify(&b)

			err := ValidateExecutableBudget(b)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !errors.Is(err, ErrInvalidExecutionBudget) {
				t.Fatalf("expected ErrInvalidExecutionBudget, got: %v", err)
			}
			if !errors.Is(err, agentbudget.ErrInvalidRequest) {
				t.Fatalf("expected agentbudget.ErrInvalidRequest, got: %v", err)
			}
		})
	}
}

// TestValidateExecutableBudget_USDRepresentationEdge tests representation floors
// (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 8).
func TestValidateExecutableBudget_USDRepresentationEdge(t *testing.T) {
	t.Run("positive_float_converts_to_zero_nanos_fails", func(t *testing.T) {
		b := validTestBudget()
		// 1e-10 USD converts to 0 nanos via modelpricing.USDFromDollars
		b.MaxUSD = 0.0000000001
		if nanos := modelpricing.USDFromDollars(b.MaxUSD); nanos != 0 {
			t.Fatalf("precondition failed: expected 0 nanos, got %d", nanos)
		}

		err := ValidateExecutableBudget(b)
		if err == nil {
			t.Fatal("expected error for sub-nano USD amount converting to 0, got nil")
		}
		if !errors.Is(err, ErrInvalidExecutionBudget) {
			t.Fatalf("expected ErrInvalidExecutionBudget, got: %v", err)
		}
		if !errors.Is(err, agentbudget.ErrInvalidRequest) {
			t.Fatalf("expected agentbudget.ErrInvalidRequest, got: %v", err)
		}
	})

	t.Run("one_nano_usd_accepted", func(t *testing.T) {
		b := validTestBudget()
		// 1e-9 USD converts to exactly 1 nano
		b.MaxUSD = 0.000000001
		if nanos := modelpricing.USDFromDollars(b.MaxUSD); nanos != 1 {
			t.Fatalf("precondition failed: expected 1 nano, got %d", nanos)
		}

		if err := ValidateExecutableBudget(b); err != nil {
			t.Fatalf("expected 1 nano USD to be valid, got: %v", err)
		}
	})
}

// TestValidateExecutableBudget_MinimumExecutableBudget proves the canonical minimal
// executable shape succeeds (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 9).
func TestValidateExecutableBudget_MinimumExecutableBudget(t *testing.T) {
	minBudget := BudgetRecommendation{
		MaxUSD:        0.000000001, // 1 nano
		MaxTokens:     1,
		MaxModelCalls: 1,
		MaxWallTimeMS: 1,
		MaxDepth:      1,
		MaxRetries:    1,
		MaxSubagents:  1,
	}

	if err := ValidateExecutableBudget(minBudget); err != nil {
		t.Fatalf("minimum executable budget failed validation: %v", err)
	}

	limits, err := ToAgentBudgetLimits(minBudget)
	if err != nil {
		t.Fatalf("ToAgentBudgetLimits failed for minimum budget: %v", err)
	}
	if err := limits.Validate(); err != nil {
		t.Fatalf("agentbudget.Limits.Validate failed for converted minimum budget: %v", err)
	}
}

// TestToAgentBudgetLimits_CanonicalDelegation proves Campaign validator delegates directly
// to agentbudget.Limits.Validate (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 10).
func TestToAgentBudgetLimits_CanonicalDelegation(t *testing.T) {
	b := validTestBudget()
	limits, err := ToAgentBudgetLimits(b)
	if err != nil {
		t.Fatalf("unexpected error converting valid budget: %v", err)
	}
	if err := limits.Validate(); err != nil {
		t.Fatalf("expected limits.Validate() == nil, got: %v", err)
	}

	// For an invalid budget, verify the wrapped cause is AgentBudget validation failure
	invalid := validTestBudget()
	invalid.MaxSubagents = 0
	_, err = ToAgentBudgetLimits(invalid)
	if err == nil {
		t.Fatal("expected error for zero subagents, got nil")
	}
	if !errors.Is(err, ErrInvalidExecutionBudget) {
		t.Fatalf("expected ErrInvalidExecutionBudget, got: %v", err)
	}
	if !errors.Is(err, agentbudget.ErrInvalidRequest) {
		t.Fatalf("expected agentbudget.ErrInvalidRequest, got: %v", err)
	}
}

// TestValidateFinanceReviewOutput_Matrix validates the host-side Finance validation matrix
// (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 sections 4 & 11).
func TestValidateFinanceReviewOutput_Matrix(t *testing.T) {
	validBudget := validTestBudget()
	zeroSubagentsBudget := validTestBudget()
	zeroSubagentsBudget.MaxSubagents = 0

	tests := []struct {
		name      string
		output    FinanceReviewOutput
		wantValid bool
		wantErrIs error
	}{
		{
			name: "recommended_with_nil_budget_invalid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictRecommended),
				RecommendedBudget: nil,
			},
			wantValid: false,
			wantErrIs: ErrInvalidExecutionBudget,
		},
		{
			name: "recommended_with_valid_budget_valid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictRecommended),
				RecommendedBudget: &validBudget,
			},
			wantValid: true,
		},
		{
			name: "recommended_with_zero_subagents_invalid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictRecommended),
				RecommendedBudget: &zeroSubagentsBudget,
			},
			wantValid: false,
			wantErrIs: ErrInvalidExecutionBudget,
		},
		{
			name: "changes_requested_with_nil_budget_valid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictChangesRequested),
				RecommendedBudget: nil,
			},
			wantValid: true,
		},
		{
			name: "not_recommended_with_nil_budget_valid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictNotRecommended),
				RecommendedBudget: nil,
			},
			wantValid: true,
		},
		{
			name: "insufficient_data_with_nil_budget_valid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictInsufficientData),
				RecommendedBudget: nil,
			},
			wantValid: true,
		},
		{
			name: "non_recommended_with_invalid_non_nil_budget_invalid",
			output: FinanceReviewOutput{
				Verdict:           string(VerdictChangesRequested),
				RecommendedBudget: &zeroSubagentsBudget,
			},
			wantValid: false,
			wantErrIs: ErrInvalidExecutionBudget,
		},
		{
			name: "invalid_verdict_invalid",
			output: FinanceReviewOutput{
				Verdict:           "unknown_verdict",
				RecommendedBudget: &validBudget,
			},
			wantValid: false,
			wantErrIs: ErrInvalidVerdict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFinanceReviewOutput(tt.output, permissiveTestRequirements())
			if tt.wantValid {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("expected error wrapping %v, got: %v", tt.wantErrIs, err)
				}
			}
		})
	}
}

// TestRenderFinanceContractInstructions_ContractPinned proves the Finance prompt
// explicitly instructs strictly positive budgets and does not contain zero examples
// (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 24).
func TestRenderFinanceContractInstructions_ContractPinned(t *testing.T) {
	instructions := renderFinanceContractInstructions(permissiveTestRequirements())

	requiredPhrases := []string{
		"recommended_budget MUST be present",
		"strictly positive",
		"max_subagents must be at least 1",
		"max_retries must be at least 1",
		"ceilings the execution may not exceed",
		"NOT a valid way to say \"none\"",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(instructions, phrase) {
			t.Errorf("prompt instructions missing required rule phrase: %q", phrase)
		}
	}

	forbiddenPatterns := []string{
		"\"max_subagents\": 0",
		"\"max_retries\": 0",
		"\"max_depth\": 0",
		"\"max_tokens\": 0",
		"\"max_model_calls\": 0",
		"\"max_wall_time_ms\": 0",
	}

	for _, forbidden := range forbiddenPatterns {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("prompt instructions contain forbidden zero ceiling pattern: %q", forbidden)
		}
	}
}

// TestExecutableBudget_ErrorChain_PreservesAgentBudgetErrInvalidRequest proves that
// invalid budgets preserve both ErrInvalidExecutionBudget and agentbudget.ErrInvalidRequest
// under errors.Is (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 merge review addendum).
func TestExecutableBudget_ErrorChain_PreservesAgentBudgetErrInvalidRequest(t *testing.T) {
	b := validTestBudget()
	b.MaxSubagents = 0

	err := ValidateExecutableBudget(b)
	if err == nil {
		t.Fatal("expected error for max_subagents = 0, got nil")
	}
	if !errors.Is(err, ErrInvalidExecutionBudget) {
		t.Fatalf("expected errors.Is(err, ErrInvalidExecutionBudget) == true, got: %v", err)
	}
	if !errors.Is(err, agentbudget.ErrInvalidRequest) {
		t.Fatalf("expected errors.Is(err, agentbudget.ErrInvalidRequest) == true, got: %v", err)
	}

	_, err = ToAgentBudgetLimits(b)
	if err == nil {
		t.Fatal("expected error from ToAgentBudgetLimits for max_subagents = 0, got nil")
	}
	if !errors.Is(err, ErrInvalidExecutionBudget) {
		t.Fatalf("expected errors.Is(err, ErrInvalidExecutionBudget) == true, got: %v", err)
	}
	if !errors.Is(err, agentbudget.ErrInvalidRequest) {
		t.Fatalf("expected errors.Is(err, agentbudget.ErrInvalidRequest) == true, got: %v", err)
	}
}
