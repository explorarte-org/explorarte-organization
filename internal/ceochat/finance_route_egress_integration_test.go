//go:build integration

package ceochat_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

const financeRouteRoleID = "negocio/administrador_financiero"

// The test #249 lacked. The real canonical documents are synchronized into the real database
// (newChatFixture), and the campaign Finance review's EFFECTIVE binding -- what dispatch resolves,
// not what a YAML file says -- is walked to the egress boundary with the inputs that flow really
// has: no executive correlation, so no executive scope, organizational data.
//
// It stops at the boundary on purpose: no network, no provider call. What it pins is the decision
// that killed every Finance review on 2026-09-25 (executive_scope_required, before any model call).
func TestTheFinanceReviewsEffectiveRouteIsAllowedAtTheEgressBoundary(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	var revisionID int64
	if err := f.store.Pool().QueryRow(ctx, `SELECT current_revision_id FROM organizations WHERE id=$1`, chatTestOrganization).Scan(&revisionID); err != nil {
		t.Fatalf("current revision: %v", err)
	}
	binding := func(roleID string) (policy, provider, model, transport string) {
		t.Helper()
		if err := f.store.Pool().QueryRow(ctx, `
SELECT b.policy_id, v.provider_id, v.provider_model_id, v.transport
FROM role_model_bindings b JOIN model_profile_versions v ON v.id=b.model_profile_version_id
WHERE b.organization_id=$1 AND b.organization_revision_id=$2 AND b.role_id=$3 AND b.active`,
			chatTestOrganization, revisionID, roleID).Scan(&policy, &provider, &model, &transport); err != nil {
			t.Fatalf("effective binding of %s at revision %d: %v", roleID, revisionID, err)
		}
		return
	}

	// Finance: its own policy, on gemini, and the scope gate lets it through with no scope.
	policy, provider, model, transport := binding(financeRouteRoleID)
	if policy != "finance.reviewer" || provider != "gemini" || model != "gemini-3.5-flash-lite" || transport != "http_adapter" {
		t.Fatalf("%s resolves to policy=%s %s/%s over %s", financeRouteRoleID, policy, provider, model, transport)
	}
	scope := modelegress.ExecutiveScopeMarker(financeRouteRoleID, "department_worker", "campaign:proposal:30", "task:1310")
	if scope != "" {
		t.Fatalf("a campaign Finance review derived executive scope %q", scope)
	}
	if reason, allowed := modelegress.ValidateExecutiveScope(provider, transport, []string{"organizational"}, scope, false); !allowed {
		t.Fatalf("the Finance review is denied at the scope gate: %s", reason)
	}

	// The real egress rules allow the same provider and class.
	// The egress rules are the REAL ones from docs/canonical/model-egress-policy.yaml, evaluated in
	// memory. This test deliberately does not write a policy version into the shared integration
	// database: versions and hashes there are global across suites, and a test that binds one
	// collides with every other run that did the same.
	routing, err := modelruntime.LoadCanonicalRouting(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	knownProviders := make([]string, 0, len(routing.Policies))
	for _, routed := range routing.Policies {
		knownProviders = append(knownProviders, routed.Provider)
	}
	canonicalPolicy, err := modelegress.LoadCanonicalPolicy(filepath.Join("..", "..", "docs", "canonical"), modelegress.ProductiveLoadOptions(knownProviders))
	if err != nil {
		t.Fatal(err)
	}
	if canonicalPolicy.PolicyVersion != 13 {
		t.Fatalf("docs/canonical/model-egress-policy.yaml policy_version=%d, want 13", canonicalPolicy.PolicyVersion)
	}
	hash := strings.Repeat("a", 64)
	resolved := modelegress.ResolvedPolicy{
		Version:                modelegress.PolicyVersion{ID: 1, PolicyVersion: canonicalPolicy.PolicyVersion, CanonicalHash: hash, Status: "materialized"},
		OrganizationRevisionID: revisionID, CanonicalHash: hash, DefaultAction: modelegress.EffectDeny, Rules: canonicalPolicy.Rules,
	}
	decision, err := modelegress.NewEvaluator().Evaluate(modelegress.EvaluationRequest{
		ProviderID: provider, ProviderTransport: transport, OrganizationRevisionID: revisionID, Policy: resolved,
		ContextClassifications: []string{"organizational"},
	})
	if err != nil || decision.Effect != modelegress.EffectAllow {
		t.Fatalf("the canonical egress policy refuses the Finance review's route: %+v %v", decision, err)
	}

	// The executive departments stay on deepseek and are allowed WITH the scope their flow derives.
	for _, tc := range []struct{ role, purpose string }{
		{"ingenieria_ia/orquestador", "department_plan"},
		{"ingenieria_ia/qa", "department_worker"},
	} {
		_, dsProvider, dsModel, dsTransport := binding(tc.role)
		if dsProvider != "deepseek" || dsModel != "deepseek-flash" {
			t.Fatalf("%s resolves to %s/%s, want deepseek/deepseek-flash", tc.role, dsProvider, dsModel)
		}
		dsScope := modelegress.ExecutiveScopeMarker(tc.role, tc.purpose, "executive:abc", "task:12")
		if reason, allowed := modelegress.ValidateExecutiveScope(dsProvider, dsTransport, []string{"organizational"}, dsScope, false); !allowed {
			t.Fatalf("%s/%s denied inside an executive run: %s", tc.role, tc.purpose, reason)
		}
		dsDecision, evalErr := modelegress.NewEvaluator().Evaluate(modelegress.EvaluationRequest{
			ProviderID: dsProvider, ProviderTransport: dsTransport, OrganizationRevisionID: revisionID, Policy: resolved,
			ContextClassifications: []string{"organizational"},
		})
		if evalErr != nil || dsDecision.Effect != modelegress.EffectAllow {
			t.Fatalf("the canonical egress policy refuses %s: %+v %v", tc.role, dsDecision, evalErr)
		}
	}
}
