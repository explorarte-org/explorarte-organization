package bootstrap

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// The per-run agent budget bounds an execution tree. Its ceilings were a code
// constant, which meant a deployment could not state its own campaign budget
// without changing the binary -- and the token ceiling silently became the
// real limit while the dollar ceiling went unused.
//
// That is not hypothetical. A run stopped at 510,917 of 500,000 tokens having
// spent $0.29 of $5.00: 68% of the token budget against 5.8% of the money.
// The dollar figure is the one an owner actually reasons about, so the token
// ceiling must be derived from it rather than set independently.
//
// Deriving it means dividing the dollar budget by the CHEAPEST blended rate
// any model in the tree can charge. Anything tighter makes tokens the binding
// constraint again for a run that happens to use a cheaper model, which is the
// failure being fixed. The token ceiling's remaining job is to stop a runaway
// loop, not to price the work.
const (
	maxUSDEnv    = "ORG_EXECUTIVE_AGENT_BUDGET_MAX_USD"
	maxTokensEnv = "ORG_EXECUTIVE_AGENT_BUDGET_MAX_TOKENS"
)

// agentBudgetLimits starts from the compiled defaults and overrides only what
// the deployment states. An unset variable keeps the default; a set but
// unusable one fails closed, because a budget that silently fell back to a
// smaller ceiling than the operator asked for would stop a run for a reason
// nobody chose.
func agentBudgetLimits() (agentbudget.Limits, error) {
	limits := agentbudget.DefaultLimits()

	if raw, ok := os.LookupEnv(maxUSDEnv); ok && strings.TrimSpace(raw) != "" {
		dollars, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return agentbudget.Limits{}, fmt.Errorf("%s: %w", maxUSDEnv, err)
		}
		// A zero or negative ceiling would authorize nothing while looking
		// like configuration; an absurd one is more likely a typo than an
		// intention.
		if dollars <= 0 || dollars > 10_000 {
			return agentbudget.Limits{}, fmt.Errorf("%s: %v is outside the allowed range (0, 10000]", maxUSDEnv, dollars)
		}
		limits.MaxUSD = modelpricing.USDFromDollars(dollars)
	}

	if raw, ok := os.LookupEnv(maxTokensEnv); ok && strings.TrimSpace(raw) != "" {
		tokens, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return agentbudget.Limits{}, fmt.Errorf("%s: %w", maxTokensEnv, err)
		}
		if tokens <= 0 {
			return agentbudget.Limits{}, fmt.Errorf("%s: %d must be positive", maxTokensEnv, tokens)
		}
		limits.MaxTokens = tokens
	}

	return limits, nil
}
