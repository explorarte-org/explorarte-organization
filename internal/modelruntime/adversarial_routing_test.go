package modelruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The whole point of this slice is that Grok is a consequence of the
// reviewer's binding, never an instruction anybody wrote down. These tests
// follow the chain the runtime actually walks:
//
//	role -> model_policy -> profile -> provider/model
//
// and then assert that no shortcut around it exists.

func adversarialRouting(t *testing.T) CanonicalRouting {
	t.Helper()
	routing, err := LoadCanonicalRouting(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	return routing
}

func TestReviewerRoleResolvesToTheXAIProfileThroughRoutingAlone(t *testing.T) {
	routing := adversarialRouting(t)
	policy, exists := routing.Policies["research.adversarial_review"]
	if !exists {
		t.Fatal("research.adversarial_review is missing from canonical routing")
	}
	if policy.Provider != "xai" {
		t.Fatalf("provider=%q", policy.Provider)
	}
	if policy.Transport != TransportHTTP {
		t.Fatalf("transport=%q", policy.Transport)
	}
	// The model id is owner-resolved. It is pinned here so a silent edit of
	// the canonical document cannot re-point the adversarial reviewer at a
	// different model without this test failing.
	if policy.Model != "grok-4.6" {
		t.Fatalf("model=%q", policy.Model)
	}
	// xAI accepts only none/low/medium/high; the adapter rejects anything
	// else before the request, so a routing document carrying this repo's
	// usual "xhigh" would fail closed rather than 400 at the provider.
	if policy.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort=%q", policy.ReasoningEffort)
	}

	reviewer := RoleRef{
		ID: "investigacion/revisor_adversarial", ModelPolicy: "research.adversarial_review",
		AuthorityClass: "transversal_audit", UnitID: "investigacion",
		// Disabled and non-executable, matching the canonical catalog's
		// proposed_profile_required status.
		Enabled: false, Executable: false,
	}
	author := RoleRef{ID: "ingenieria_ia/orquestador", ModelPolicy: "department.leader", Enabled: true, Executable: true}

	plan, err := BuildRegistryPlan([]RoleRef{reviewer, author}, OrganizationRef{ID: "explorarte", RevisionID: 7}, routing)
	if err != nil {
		t.Fatalf("BuildRegistryPlan: %v", err)
	}

	var binding *RoleBinding
	for i := range plan.Bindings {
		if plan.Bindings[i].RoleID == reviewer.ID {
			binding = &plan.Bindings[i]
		}
	}
	if binding == nil {
		t.Fatal("no binding was produced for the reviewer role")
	}
	if binding.PolicyID != "research.adversarial_review" || binding.ProfileID != "research.adversarial_review" {
		t.Fatalf("binding=%+v", *binding)
	}
	// The binding exists but is inactive: the role is not activated yet, and
	// an inactive binding is the fail-closed state, not a missing one.
	if binding.Active {
		t.Fatal("reviewer binding is active while the role is still proposed")
	}

	var version *ProfileVersion
	for i := range plan.Versions {
		if plan.Versions[i].ProfileID == "research.adversarial_review" {
			version = &plan.Versions[i]
		}
	}
	if version == nil {
		t.Fatal("no profile version for the reviewer")
	}
	if version.ProviderID != "xai" {
		t.Fatalf("reviewer resolved to provider %q", version.ProviderID)
	}

	// And the design author resolves somewhere else entirely. Reviewer and
	// author sharing a provider would make the second opinion the same
	// opinion.
	for i := range plan.Versions {
		if plan.Versions[i].ProfileID == "leader-primary" && plan.Versions[i].ProviderID == "xai" {
			t.Fatal("the design author also routes to xai")
		}
	}
}

// No role other than the reviewer may reach xai through canonical routing.
func TestNoOtherPolicyRoutesToXAI(t *testing.T) {
	routing := adversarialRouting(t)
	for id, policy := range routing.Policies {
		if policy.Provider == "xai" && id != "research.adversarial_review" {
			t.Fatalf("policy %q also routes to xai", id)
		}
	}
}

func TestXAIAdapterAvailabilityIsCompiledNotInferred(t *testing.T) {
	status, dispatchable := compiledAdapterAvailability(routingPolicy{Transport: TransportHTTP, Provider: "xai"})
	if status != AdapterAvailable || !dispatchable {
		t.Fatalf("compiled xai adapter reported unavailable: %v/%v", status, dispatchable)
	}
	// A provider with no compiled adapter fails closed, and so does xai over
	// a transport it does not implement.
	for _, policy := range []routingPolicy{
		{Transport: TransportHTTP, Provider: "anthropic"},
		{Transport: "cli_adapter", Provider: "xai"},
		{Transport: TransportFake, Provider: "xai"},
	} {
		status, dispatchable = compiledAdapterAvailability(policy)
		if status != AdapterUnavailable || dispatchable {
			t.Fatalf("%+v was reported dispatchable", policy)
		}
	}
}

// The Executive must not contain provider knowledge. If this ever fails, the
// routing indirection has been bypassed by a literal somewhere.
func TestExecutiveContainsNoProviderLiterals(t *testing.T) {
	forbidden := []string{"xai", "grok", "deepseek", "gpt-5.6-luna", "openai_responses"}
	root := filepath.Join("..", "executive")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		// Tests may name providers when asserting that they are refused.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lowered := strings.ToLower(string(body))
		for _, needle := range forbidden {
			if strings.Contains(lowered, needle) {
				t.Errorf("%s mentions provider %q -- routing must decide this, not the Executive", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
