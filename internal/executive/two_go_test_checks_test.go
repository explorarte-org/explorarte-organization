package executive

import (
	"strings"
	"testing"
)

// A mission that changed Go packages records two GO_TEST checks (the changed packages, then the
// module); each must succeed. Every other gate still appears exactly once.
func TestTwoGoTestChecksAreAcceptedOnlyIfBothSucceed(t *testing.T) {
	withChecks := func(extra ...map[string]any) TaskRecord {
		mission := codeRunnerTaskForTest()
		for i, evidence := range mission.Evidence {
			if strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
				checks := evidence.Metadata["checks_run"].([]any)
				for _, check := range extra {
					checks = append(checks, check)
				}
				evidence.Metadata["checks_run"] = checks
				mission.Evidence[i] = evidence
			}
		}
		return mission
	}
	if _, _, err := verifiedCodeRunnerEvidence(withChecks(map[string]any{"type": "GO_TEST", "success": true})); err != nil {
		t.Fatalf("two successful GO_TEST runs were refused: %v", err)
	}
	if _, _, err := verifiedCodeRunnerEvidence(withChecks(map[string]any{"type": "GO_TEST", "success": false})); err == nil {
		t.Fatal("a failed second GO_TEST run was accepted")
	}
	if _, _, err := verifiedCodeRunnerEvidence(withChecks(map[string]any{"type": "GO_BUILD", "success": true})); err == nil || !strings.Contains(err.Error(), "duplicate GO_BUILD") {
		t.Fatalf("a duplicate GO_BUILD check = %v, want refused", err)
	}
}
