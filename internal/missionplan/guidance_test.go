package missionplan

import (
	"strings"
	"testing"
)

func TestExecutionGuidanceUsesEnforcedPolicy(t *testing.T) {
	for scope, prefixes := range scopePrefixes {
		text, err := ExecutionGuidance(scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, entries := range [][]string{prefixes, forbiddenPrefixes, kernelGovernancePrefixes, forbiddenExact} {
			for _, entry := range entries {
				if !strings.Contains(text, `"`+entry+`"`) {
					t.Fatalf("%s missing enforced path %q", scope, entry)
				}
			}
		}
		for _, gate := range RequiredGates() {
			if !strings.Contains(text, `"`+string(gate.Type)+`"`) {
				t.Fatalf("missing gate %s", gate.Type)
			}
		}
		if !strings.Contains(text, "Independent review") {
			t.Fatal("must not imply gates authorize promotion")
		}
	}
	if _, err := ExecutionGuidance("unrestricted"); err == nil {
		t.Fatal("unknown scope accepted")
	}
}
