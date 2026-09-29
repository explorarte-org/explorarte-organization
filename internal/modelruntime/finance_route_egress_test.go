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

// Regression 1: the Finance role resolves to its own policy. Since 2026-09-28 (Gemini retired from
// routing after repeated HTTP 503s) that policy is DeepSeek Flash, reached through its own narrow
// egress scope.
func TestTheFinanceReviewerHasItsOwnPolicyOnDeepseek(t *testing.T) {
	provider, model, transport, policy := routeOf(t, financeRoleID)
	if policy != financePolicyID {
		t.Fatalf("%s is bound to %q, want its own policy %q (it must not ride department.worker)", financeRoleID, policy, financePolicyID)
	}
	if provider != "deepseek" || model != "deepseek-flash" || transport != TransportHTTP {
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

// Regression 4: the campaign Finance review, in the flow it really runs in -- the context's
// department_worker purpose, on a task correlated to the CEO chat turn that asked for it -- derives
// the finance review scope, and its route is allowed at the scope gate with it.
func TestTheFinanceReviewPassesTheScopeGateInItsRealFlow(t *testing.T) {
	provider, _, transport, _ := routeOf(t, financeRoleID)
	scope := modelegress.ExecutiveScopeMarker(financeRoleID, "department_worker", "ceochat:6", "task:2183")
	if scope != modelegress.ScopeFinanceReview {
		t.Fatalf("the Finance review derived scope %q, want %q", scope, modelegress.ScopeFinanceReview)
	}
	if reason, allowed := modelegress.ValidateExecutiveScope(provider, string(transport), []string{"organizational"}, scope, false); !allowed {
		t.Fatalf("Finance review on %s denied at the scope gate: %s", provider, reason)
	}
}

// Regression 5: outside that flow the Finance role derives no scope, and DeepSeek refuses it --
// the exact denial production returned on 2026-09-25.
func TestAFinanceReviewOutsideItsFlowIsDeniedForWantOfScope(t *testing.T) {
	for _, flow := range [][2]string{{financePurposeRaw, "campaign:proposal:30"}, {"department_worker", "chat:6"}, {"department_worker", ""}} {
		scope := modelegress.ExecutiveScopeMarker(financeRoleID, flow[0], flow[1], "task:1310")
		reason, allowed := modelegress.ValidateExecutiveScope("deepseek", "http_adapter", []string{"organizational"}, scope, false)
		if allowed || reason != "executive_scope_required" {
			t.Fatalf("flow %v: allowed=%v reason=%q, want the executive_scope_required denial", flow, allowed, reason)
		}
	}
}

// Regression 5b: every role's route is allowed with the scope its own flow derives: the Finance
// reviewer in its chat-turn flow, the executive stages inside an executive run.
func TestEveryRoleIsAllowedWithTheScopeItsFlowDerives(t *testing.T) {
	for _, tc := range []struct{ role, purpose, correlation string }{
		{financeRoleID, "department_worker", "ceochat:6"},
		{"ingenieria_ia/orquestador", "department_plan", "executive:abc"},
		{"ingenieria_ia/orquestador", "department_review", "executive:abc"},
		{"ingenieria_ia/orquestador", "implementation_plan", "executive:abc"},
		{"ingenieria_ia/qa", "department_worker", "executive:abc"},
		{"ingenieria_ia/arquitecto_software", "department_worker", "executive:abc"},
	} {
		provider, _, transport, _ := routeOf(t, tc.role)
		scope := modelegress.ExecutiveScopeMarker(tc.role, tc.purpose, tc.correlation, "task:12")
		if reason, allowed := modelegress.ValidateExecutiveScope(provider, string(transport), []string{"organizational"}, scope, false); !allowed {
			t.Fatalf("%s/%s on %s denied in its own flow: %s", tc.role, tc.purpose, provider, reason)
		}
	}
}

// Regression 6: Gemini is out of routing and out of the egress policy (version 14), and the
// Finance review's real provider/class is allowed by the evaluator.
func TestGeminiIsRetiredFromRoutingAndEgress(t *testing.T) {
	routing, err := LoadCanonicalRouting(canonicalDirForFinanceTest())
	if err != nil {
		t.Fatal(err)
	}
	known := make([]string, 0, len(routing.Policies))
	for id, policy := range routing.Policies {
		if policy.Provider == "gemini" {
			t.Fatalf("policy %s still routes to gemini", id)
		}
		known = append(known, policy.Provider)
	}
	policy, err := modelegress.LoadCanonicalPolicy(canonicalDirForFinanceTest(), modelegress.ProductiveLoadOptions(known))
	if err != nil {
		t.Fatal(err)
	}
	if policy.PolicyVersion != 14 {
		t.Fatalf("policy_version=%d, want 14", policy.PolicyVersion)
	}
	for _, rule := range policy.Rules {
		if rule.ProviderID == "gemini" {
			t.Fatalf("gemini egress rule %+v survived its retirement", rule)
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
