package bootstrap

import (
	"fmt"
	"os"
	"strings"
)

// The per-run agent budget bounds a campaign's execution tree, and it used to
// be read from this process's environment.
//
// That is why a campaign could be born under ceilings nobody chose. Submit
// created the root budget from whatever limits its own process had resolved,
// and the durable row is ON CONFLICT (task_id) DO NOTHING, so the first writer
// won permanently. A campaign submitted by a CLI without these variables was
// born with the package defaults and kept them for its whole life, while the
// Executive runtime driving it had been started with completely different
// ones. Neither process was wrong; there were simply two answers to one
// question, and the race picked one.
//
// The ceilings are now stated at submission and recorded durably with the
// campaign's root, so the runtime holds none at all and has nothing to
// diverge from. See executive.CampaignBudget.
const (
	deprecatedMaxUSDEnv    = "ORG_EXECUTIVE_AGENT_BUDGET_MAX_USD"
	deprecatedMaxTokensEnv = "ORG_EXECUTIVE_AGENT_BUDGET_MAX_TOKENS"
)

// rejectDeprecatedAgentBudgetEnv fails startup when a deployment still sets the
// old variables.
//
// Ignoring them would be the worse failure. An operator who exports a $17
// ceiling and gets a $5 campaign has been told nothing, and would find out
// when a run stops early for a reason nobody chose -- which is precisely how
// the original defect presented. Refusing to start says it once, at the
// moment it can still be acted on.
func rejectDeprecatedAgentBudgetEnv() error {
	stale := make([]string, 0, 2)
	for _, name := range []string{deprecatedMaxUSDEnv, deprecatedMaxTokensEnv} {
		if raw, ok := os.LookupEnv(name); ok && strings.TrimSpace(raw) != "" {
			stale = append(stale, name)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s is set but no longer configures anything: a campaign's ceilings are now stated when it is submitted and recorded durably with its root, so that they cannot depend on which process submitted it. Unset it and pass the ceilings to `orgctl executive submit` instead",
		strings.Join(stale, " and "))
}
