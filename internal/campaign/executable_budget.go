package campaign

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// ToAgentBudgetLimits deterministically converts a campaign budget
// recommendation into the canonical agentbudget.Limits shape Executive
// actually enforces, and validates it through the SAME agentbudget.Limits.
// Validate Executive itself will run -- this is the one seam through which
// Campaign's seven budget dimensions become AgentBudget's seven budget
// dimensions, so Finance, Approval, and Promotion all delegate here
// instead of hand-rolling their own positivity checks, and Campaign cannot
// silently drift from AgentBudget's own contract if it ever changes.
//
// This never relaxes or reinterprets AgentBudget: zero is not "unlimited"
// and not "disabled", it is simply not executable under the current
// AgentBudget model -- a campaign submitted to Executive is an execution
// tree that may require at least one downstream delegation
// (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 4).
func ToAgentBudgetLimits(rec BudgetRecommendation) (agentbudget.Limits, error) {
	limits := agentbudget.Limits{
		MaxUSD:        modelpricing.USDFromDollars(rec.MaxUSD),
		MaxTokens:     rec.MaxTokens,
		MaxModelCalls: int64(rec.MaxModelCalls),
		MaxWallTimeMS: rec.MaxWallTimeMS,
		MaxDepth:      int64(rec.MaxDepth),
		MaxRetries:    int64(rec.MaxRetries),
		MaxSubagents:  int64(rec.MaxSubagents),
	}
	if err := limits.Validate(); err != nil {
		return agentbudget.Limits{}, fmt.Errorf("%w: %w", ErrInvalidExecutionBudget, err)
	}
	return limits, nil
}

// ValidateExecutableBudget reports whether rec can be promoted into a real
// Executive campaign budget. It is a thin convenience over
// ToAgentBudgetLimits for callers that only need the error, never the
// converted value.
func ValidateExecutableBudget(rec BudgetRecommendation) error {
	_, err := ToAgentBudgetLimits(rec)
	return err
}
