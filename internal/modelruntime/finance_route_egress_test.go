package modelruntime

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	organizationregistry "github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// Root cause of the 2026-09-25 incident: negocio/administrador_financiero was bound to
// department.worker, so moving department.worker to deepseek dragged the campaign Finance review
// with it. DeepSeek requires a backend-derived executive scope, which only exists inside an
// executive run (correlation "executive:*"); the Finance review runs BEFORE a campaign is
// promoted, so it never has one, and every review failed with executive_scope_required. No test
// looked at the egress boundary of the roles bound to a policy when that policy's provider changed.
//
// These tests read the REAL canonical documents and walk the same decisions the runtime makes:
// role -> model_policy -> provider/transport (LoadCanonicalRouting), then the executive scope gate
// (ValidateExecutiveScope) with the scope the role's own flow actually derives
// (ExecutiveScopeMarker), then the egress policy's allow rule for that provider and class.

const (
	financeRoleID     = "negocio/administrador_financiero"
	financePolicyID   = "finance.reviewer"
	financePurposeRaw = "campaign.financial_review"
)

func canonicalDirForFinanceTest() string {
	return filepath.Join("..", "..", "docs", "canonical")
}

func roleModelPolicies(t *testing.T) map[string]string {
	t.Helper()
	loader, err := organizationregistry.NewLoader(canonicalDirForFinanceTest())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, report, err := loader.Load()
	if err != nil {
		t.Fatalf("load canonical registry: %v %+v", err, report)
	}
	policies := make(map[string]string, len(snapshot.Roles))
	for _, role := range snapshot.Roles {
		if role.ModelPolicy != nil {
			policies[role.ID] = *role.ModelPolicy
		}
	}
	return policies
}

func routeOf(t *testing.T, roleID string) (provider, model string, transport Transport, policy string) {
	t.Helper()
	policy, ok := roleModelPolicies(t)[roleID]
	if !ok {
		t.Fatalf("role %s has no model_policy in the canonical catalog", roleID)
	}
	routing, err := LoadCanonicalRouting(canonicalDirForFinanceTest())
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := routing.Policies[policy]
	if !ok {
		t.Fatalf("role %s names policy %q, which model-routing.yaml does not define", roleID, policy)
	}
	return resolved.Provider, resolved.Model, resolved.Transport, policy
}

// Regression 1: the Finance role resolves to its own policy and to gemini-3.5-flash-lite.
func TestTheFinanceReviewerHasItsOwnPolicyOnGemini(t *testing.T) {
	provider, model, transport, policy := routeOf(t, financeRoleID)
	if policy != financePolicyID {
		t.Fatalf("%s is bound to %q, want its own policy %q (it must not ride department.worker)", financeRoleID, policy, financePolicyID)
	}
	if provider != "gemini" || model != "gemini-3.5-flash-lite" || transport != TransportHTTP {
		t.Fatalf("%s resolves to %s/%s over %s", financeRoleID, provider, model, transport)
	}
}

// Regressions 2 and 3: the executive departments are still on DeepSeek Flash -- the variable this
// work exists to keep -- and every role bound to those policies stays on it.
func TestTheDepartmentPoliciesStayOnDeepseekFlash(t *testing.T) {
	routing, err := LoadCanonicalRouting(canonicalDirForFinanceTest())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"department.leader", "department.worker"} {
		policy := routing.Policies[id]
		if policy.Provider != "deepseek" || policy.Model != "deepseek-flash" || policy.Transport != TransportHTTP {
			t.Fatalf("%s = %s/%s over %s, want deepseek/deepseek-flash", id, policy.Provider, policy.Model, policy.Transport)
		}
	}
	checked := 0
	for roleID, policy := range roleModelPolicies(t) {
		if (policy == "department.leader" || policy == "department.worker") && strings.HasPrefix(roleID, "ingenieria_ia/") {
			if provider, model, _, _ := routeOf(t, roleID); provider != "deepseek" || model != "deepseek-flash" {
				t.Fatalf("%s resolves to %s/%s", roleID, provider, model)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no ingenieria_ia role is bound to a department policy; the check proved nothing")
	}
}

// Regression 4: the campaign Finance review, with the correlation it really has (NOT executive:*),
// derives no scope, and its route must still be allowed at the scope gate.
func TestTheFinanceReviewPassesTheScopeGateWithoutAnExecutiveCorrelation(t *testing.T) {
	provider, _, transport, _ := routeOf(t, financeRoleID)
	for _, purpose := range []string{financePurposeRaw, "department_worker"} {
		for _, correlation := range []string{"campaign:proposal:30", "chat:6", ""} {
			scope := modelegress.ExecutiveScopeMarker(financeRoleID, purpose, correlation, "task:1310")
			if scope != "" {
				t.Fatalf("a non-executive Finance flow derived scope %q (purpose=%q correlation=%q)", scope, purpose, correlation)
			}
			reason, allowed := modelegress.ValidateExecutiveScope(provider, string(transport), []string{"organizational"}, scope, false)
			if !allowed {
				t.Fatalf("Finance review on %s denied at the scope gate (%s): purpose=%q correlation=%q", provider, reason, purpose, correlation)
			}
		}
	}
}

// Regression 5: if Finance ever goes back to a scope-gated provider, this is what breaks -- and
// it is the exact denial production returned.
func TestAFinanceReviewOnADeepseekRouteIsDeniedForWantOfScope(t *testing.T) {
	scope := modelegress.ExecutiveScopeMarker(financeRoleID, financePurposeRaw, "campaign:proposal:30", "task:1310")
	reason, allowed := modelegress.ValidateExecutiveScope("deepseek", "http_adapter", []string{"organizational"}, scope, false)
	if allowed || reason != "executive_scope_required" {
		t.Fatalf("deepseek for a Finance review: allowed=%v reason=%q, want the executive_scope_required denial", allowed, reason)
	}
}

// Regression 5b: the same rule as a property of the real catalog -- any role that can derive no
// executive scope in its own flow must not resolve to a provider that requires one.
func TestNoScopelessRoleIsBoundToAScopeGatedProvider(t *testing.T) {
	for _, roleID := range []string{financeRoleID} {
		provider, _, transport, policy := routeOf(t, roleID)
		if reason, allowed := modelegress.ValidateExecutiveScope(provider, string(transport), []string{"organizational"}, "", false); !allowed {
			t.Fatalf("%s (policy %s) resolves to %s, which requires an executive scope its flow cannot derive: %s", roleID, policy, provider, reason)
		}
	}
	// And the executive stages, which DO derive one, are allowed on deepseek with it.
	for _, tc := range []struct{ role, purpose string }{
		{"ingenieria_ia/orquestador", "department_plan"},
		{"ingenieria_ia/orquestador", "department_review"},
		{"ingenieria_ia/orquestador", "implementation_plan"},
		{"ingenieria_ia/qa", "department_worker"},
		{"ingenieria_ia/arquitecto_software", "department_worker"},
	} {
		provider, _, transport, _ := routeOf(t, tc.role)
		scope := modelegress.ExecutiveScopeMarker(tc.role, tc.purpose, "executive:abc", "task:12")
		if reason, allowed := modelegress.ValidateExecutiveScope(provider, string(transport), []string{"organizational"}, scope, false); !allowed {
			t.Fatalf("%s/%s on %s denied in an executive run: %s", tc.role, tc.purpose, provider, reason)
		}
	}
}

// Regression 6: the egress policy allows gemini for exactly the three classes it did before the
// department move, and the Finance review's real provider/class is allowed by the evaluator.
func TestTheGeminiEgressRulesAreRestoredExactly(t *testing.T) {
	routing, err := LoadCanonicalRouting(canonicalDirForFinanceTest())
	if err != nil {
		t.Fatal(err)
	}
	known := make([]string, 0, len(routing.Policies))
	for _, policy := range routing.Policies {
		known = append(known, policy.Provider)
	}
	policy, err := modelegress.LoadCanonicalPolicy(canonicalDirForFinanceTest(), modelegress.ProductiveLoadOptions(known))
	if err != nil {
		t.Fatal(err)
	}
	if policy.PolicyVersion != 13 {
		t.Fatalf("policy_version=%d, want 13", policy.PolicyVersion)
	}
	want := map[string]string{
		"organizational": "executive_scope_gate_required_v4",
		"public":         "public_egress_approved_for_gemini_v1",
		"sanitized":      "sanitized_egress_approved_for_gemini_v1",
	}
	got := map[string]string{}
	for _, rule := range policy.Rules {
		if rule.ProviderID == "gemini" {
			if rule.Effect != modelegress.EffectAllow {
				t.Fatalf("gemini rule %+v is not an allow", rule)
			}
			got[string(rule.DataClassification)] = rule.ReasonCode
		}
	}
	if len(got) != len(want) {
		t.Fatalf("gemini rules = %v, want exactly %v", got, want)
	}
	for class, reason := range want {
		if got[class] != reason {
			t.Fatalf("gemini/%s reason=%q, want %q", class, got[class], reason)
		}
	}

	hash := strings.Repeat("a", 64)
	resolved := modelegress.ResolvedPolicy{
		Version:                modelegress.PolicyVersion{ID: 1, PolicyVersion: policy.PolicyVersion, CanonicalHash: hash, Status: "materialized"},
		OrganizationRevisionID: 7, CanonicalHash: hash, DefaultAction: modelegress.EffectDeny, Rules: policy.Rules,
	}
	provider, _, transport, _ := routeOf(t, financeRoleID)
	decision, err := modelegress.NewEvaluator().Evaluate(modelegress.EvaluationRequest{
		ProviderID: provider, ProviderTransport: string(transport), OrganizationRevisionID: 7, Policy: resolved,
		ContextClassifications: []string{"organizational"},
	})
	if err != nil || decision.Effect != modelegress.EffectAllow {
		t.Fatalf("the Finance review's route was refused by the egress policy: %+v %v", decision, err)
	}
}
