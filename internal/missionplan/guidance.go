package missionplan

import (
	"encoding/json"
	"fmt"
)

// ExecutionGuidance describes the real host policy before a model proposes a
// diff. It is guidance only: Derive and the per-mission guard still enforce it.
// Never maintain a separate prompt-only copy of the governance denylist.
func ExecutionGuidance(scope Scope) (string, error) {
	prefixes, ok := scopePrefixes[scope]
	if !ok {
		return "", fmt.Errorf("%w: unknown scope %q", ErrScopeDenied, scope)
	}
	policy := map[string]any{
		"scope":                      scope,
		"allowed_prefixes":           prefixes,
		"forbidden_prefixes":         forbiddenPrefixes,
		"kernel_governance_prefixes": kernelGovernancePrefixes,
		"forbidden_files":            forbiddenExact,
		"required_gates":             RequiredGates(),
	}
	body, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return "HOST MISSION POLICY (guidance, not an authority grant):\n" + string(body) +
		"\nDenied paths take precedence over allowed prefixes. Auditing protected code does not authorize rewriting it. " +
		"Propose changes only within this scope; do not edit the policy, gates or permissions to make a task pass. " +
		"A candidate and passing gates are not promotion approval. Independent review and authorized promotion remain separate host actions.", nil
}
