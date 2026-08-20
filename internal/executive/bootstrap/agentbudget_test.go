package bootstrap

import (
	"strings"
	"testing"
)

// These variables used to configure the campaign budget from the environment
// of whichever process happened to build a runtime. They configure nothing
// now, and the only thing worth testing is that their presence cannot be
// mistaken for configuration that still works.
func TestDeprecatedAgentBudgetEnvIsRefusedRatherThanIgnored(t *testing.T) {
	t.Run("absent is fine", func(t *testing.T) {
		t.Setenv(deprecatedMaxUSDEnv, "")
		t.Setenv(deprecatedMaxTokensEnv, "")
		if err := rejectDeprecatedAgentBudgetEnv(); err != nil {
			t.Fatalf("an unset deployment must start normally: %v", err)
		}
	})

	for _, tc := range []struct {
		name      string
		usd, toks string
		mustName  []string
	}{
		{"dollar ceiling still exported", "17", "", []string{deprecatedMaxUSDEnv}},
		{"token ceiling still exported", "", "120000000", []string{deprecatedMaxTokensEnv}},
		{"both still exported", "17", "120000000", []string{deprecatedMaxUSDEnv, deprecatedMaxTokensEnv}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(deprecatedMaxUSDEnv, tc.usd)
			t.Setenv(deprecatedMaxTokensEnv, tc.toks)
			err := rejectDeprecatedAgentBudgetEnv()
			if err == nil {
				// Silently ignoring it is the dangerous outcome: the
				// operator exported a $17 ceiling, gets a $5 campaign, and
				// learns about it when a run stops early. That is the
				// original defect wearing a different hat.
				t.Fatal("a stale ceiling must stop startup, not be ignored")
			}
			for _, name := range tc.mustName {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the refusal must name %s so an operator knows what to unset, got: %v", name, err)
				}
			}
			if !strings.Contains(err.Error(), "orgctl executive submit") {
				t.Errorf("the refusal must name the replacement, got: %v", err)
			}
		})
	}
}
