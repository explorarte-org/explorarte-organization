package bootstrap

import (
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// An unconfigured deployment keeps the compiled defaults exactly.
func TestAgentBudgetDefaultsWhenUnset(t *testing.T) {
	t.Setenv(maxUSDEnv, "")
	t.Setenv(maxTokensEnv, "")
	limits, err := agentBudgetLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits != agentbudget.DefaultLimits() {
		t.Fatalf("limits drifted from the default: %+v", limits)
	}
}

// The campaign figures: $17 across the tree, and a token ceiling derived from
// that dollar budget at the cheapest blended rate so it cannot bite first.
func TestAgentBudgetAcceptsCampaignCeilings(t *testing.T) {
	t.Setenv(maxUSDEnv, "17")
	t.Setenv(maxTokensEnv, "120000000")
	limits, err := agentBudgetLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxUSD != modelpricing.USDFromDollars(17) {
		t.Fatalf("max usd=%v", limits.MaxUSD)
	}
	if limits.MaxTokens != 120_000_000 {
		t.Fatalf("max tokens=%d", limits.MaxTokens)
	}
	// Everything else is untouched: this configures two ceilings, not the
	// whole budget shape.
	base := agentbudget.DefaultLimits()
	if limits.MaxModelCalls != base.MaxModelCalls || limits.MaxDepth != base.MaxDepth ||
		limits.MaxRetries != base.MaxRetries || limits.MaxSubagents != base.MaxSubagents ||
		limits.MaxWallTimeMS != base.MaxWallTimeMS {
		t.Fatalf("unrelated ceilings changed: %+v", limits)
	}
}

// Each variable stands alone, so a deployment can raise one without restating
// the other.
func TestAgentBudgetCeilingsAreIndependent(t *testing.T) {
	t.Setenv(maxUSDEnv, "17")
	t.Setenv(maxTokensEnv, "")
	limits, err := agentBudgetLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxUSD != modelpricing.USDFromDollars(17) || limits.MaxTokens != agentbudget.DefaultLimits().MaxTokens {
		t.Fatalf("usd-only override: %+v", limits)
	}

	t.Setenv(maxUSDEnv, "")
	t.Setenv(maxTokensEnv, "120000000")
	limits, err = agentBudgetLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxTokens != 120_000_000 || limits.MaxUSD != agentbudget.DefaultLimits().MaxUSD {
		t.Fatalf("token-only override: %+v", limits)
	}
}

// A stated but unusable ceiling fails closed. Falling back to a smaller
// default would stop a run for a limit nobody chose, which is the exact
// confusion this change exists to remove.
func TestUnusableAgentBudgetCeilingsFailClosed(t *testing.T) {
	for name, pair := range map[string][2]string{
		"usd not a number":  {"seventeen", ""},
		"usd zero":          {"0", ""},
		"usd negative":      {"-5", ""},
		"usd absurd":        {"1000000", ""},
		"tokens not an int": {"", "many"},
		"tokens zero":       {"", "0"},
		"tokens negative":   {"", "-1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(maxUSDEnv, pair[0])
			t.Setenv(maxTokensEnv, pair[1])
			if _, err := agentBudgetLimits(); err == nil {
				t.Fatalf("%s was accepted", name)
			} else if !strings.Contains(err.Error(), "ORG_EXECUTIVE_AGENT_BUDGET") {
				t.Fatalf("the error does not name the variable: %v", err)
			}
		})
	}
}

// The property that motivated the change: at the campaign ceilings, the token
// budget cannot be exhausted before the dollar budget, whichever model the
// tree happens to use. The cheapest blended rate here is DeepSeek Flash at
// 95% input, $0.147 per million tokens.
func TestTokenCeilingCannotBiteBeforeTheDollarCeiling(t *testing.T) {
	t.Setenv(maxUSDEnv, "17")
	t.Setenv(maxTokensEnv, "120000000")
	limits, err := agentBudgetLimits()
	if err != nil {
		t.Fatal(err)
	}
	const cheapestUSDPerMillionTokens = 0.147
	tokensTheDollarsBuy := int64(limits.MaxUSD.USD() / cheapestUSDPerMillionTokens * 1_000_000)
	if limits.MaxTokens < tokensTheDollarsBuy {
		t.Fatalf("the token ceiling (%d) is below what the dollar ceiling buys (%d): tokens would bind first",
			limits.MaxTokens, tokensTheDollarsBuy)
	}
}
