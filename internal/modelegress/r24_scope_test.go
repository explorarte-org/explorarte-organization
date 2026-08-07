package modelegress

import "testing"

func r24Policy() ResolvedPolicy {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rules := []Rule{
		{ProviderID: "*", DataClassification: ClassificationClinical, Effect: EffectDeny, ReasonCode: "clinical_egress_forbidden", HardDeny: true},
		{ProviderID: "*", DataClassification: ClassificationSecret, Effect: EffectDeny, ReasonCode: "secret_egress_forbidden", HardDeny: true},
	}
	for _, provider := range []string{"alibaba_token_plan_via_claude_code", "deepseek", "openai_compatible"} {
		for _, class := range []DataClassification{ClassificationPublic, ClassificationSanitized, ClassificationOrganizational} {
			rules = append(rules, Rule{ProviderID: provider, DataClassification: class, Effect: EffectAllow, ReasonCode: "executive_scope_gate_required_v3"})
		}
	}
	return ResolvedPolicy{
		Version: PolicyVersion{ID: 1, PolicyVersion: 3, CanonicalHash: hash, Status: "materialized"},
		OrganizationRevisionID: 7, CanonicalHash: hash, DefaultAction: EffectDeny, Rules: rules,
	}
}

func TestExecutiveScopeMarkerRequiresDurableExecutiveMetadata(t *testing.T) {
	if got := ExecutiveScopeMarker("empresa/ceo", "executive_ceo_plan", "executive:abc", "task:10"); got != ScopeExecutiveCEO {
		t.Fatalf("CEO scope=%q", got)
	}
	if got := ExecutiveScopeMarker("ingenieria_ia/orquestador", "department_review", "executive:abc", "task:11"); got != ScopeDepartmentLeader {
		t.Fatalf("leader scope=%q", got)
	}
	if got := ExecutiveScopeMarker("ingenieria_ia/qa", "department_worker", "executive:abc", "task:12"); got != ScopeDepartmentWorker {
		t.Fatalf("worker scope=%q", got)
	}
	for name, got := range map[string]string{
		"non executive correlation": ExecutiveScopeMarker("empresa/ceo", "executive_ceo_plan", "other:abc", "task:10"),
		"no task ref":               ExecutiveScopeMarker("empresa/ceo", "executive_ceo_plan", "executive:abc", ""),
		"owner cannot be worker":    ExecutiveScopeMarker("empresa/human", "department_worker", "executive:abc", "task:10"),
		"observer cannot be leader": ExecutiveScopeMarker("empresa/ceo_observer", "department_review", "executive:abc", "task:10"),
		"unknown purpose":           ExecutiveScopeMarker("ingenieria_ia/qa", "anything", "executive:abc", "task:10"),
	} {
		if got != "" {
			t.Fatalf("%s unexpectedly scoped as %q", name, got)
		}
	}
}

func TestValidateExecutiveScopeMatrix(t *testing.T) {
	cases := []struct {
		name      string
		provider  string
		transport string
		classes   []string
		scope     string
		allowed   bool
		reason    string
	}{
		{"ceo alibaba", "alibaba_token_plan_via_claude_code", "cli_adapter", []string{"organizational", "public"}, ScopeExecutiveCEO, true, "executive_scope_verified_ceo"},
		{"ceo missing scope", "alibaba_token_plan_via_claude_code", "cli_adapter", []string{"organizational"}, "", false, "executive_scope_required"},
		{"ceo wrong transport", "alibaba_token_plan_via_claude_code", "http_adapter", []string{"organizational"}, ScopeExecutiveCEO, false, "executive_scope_required"},
		{"leader openai organizational", "openai_compatible", "http_adapter", []string{"organizational"}, ScopeDepartmentLeader, true, "executive_scope_verified_department_leader"},
		{"leader wrong scope", "openai_compatible", "http_adapter", []string{"organizational"}, ScopeDepartmentWorker, false, "executive_scope_required"},
		{"openai public legacy", "openai_compatible", "http_adapter", []string{"public"}, "", true, "executive_scope_not_required"},
		{"worker deepseek", "deepseek", "http_adapter", []string{"sanitized", "organizational"}, ScopeDepartmentWorker, true, "executive_scope_verified_department_worker"},
		{"worker deepseek no scope", "deepseek", "http_adapter", []string{"public"}, "", false, "executive_scope_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, allowed := ValidateExecutiveScope(tc.provider, tc.transport, tc.classes, tc.scope)
			if allowed != tc.allowed || reason != tc.reason {
				t.Fatalf("allowed=%v reason=%q want allowed=%v reason=%q", allowed, reason, tc.allowed, tc.reason)
			}
		})
	}
}

func TestPolicyEvaluationStillHardDeniesSecretAndClinical(t *testing.T) {
	e := NewEvaluator()
	policy := r24Policy()
	for _, classification := range []string{"secret", "clinical"} {
		decision, err := e.Evaluate(EvaluationRequest{
			ProviderID: "alibaba_token_plan_via_claude_code", ProviderTransport: "cli_adapter",
			OrganizationRevisionID: 7, Policy: policy, ContextClassifications: []string{classification},
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Effect != EffectDeny {
			t.Fatalf("%s unexpectedly allowed: %+v", classification, decision)
		}
	}
}

func TestPolicyV3AllowDoesNotReplaceSeparateScopeGate(t *testing.T) {
	e := NewEvaluator()
	policy := r24Policy()
	decision, err := e.Evaluate(EvaluationRequest{
		ProviderID: "deepseek", ProviderTransport: "http_adapter",
		OrganizationRevisionID: 7, Policy: policy, ContextClassifications: []string{"organizational", "sanitized"},
	})
	if err != nil || decision.Effect != EffectAllow {
		t.Fatalf("classification decision=%+v err=%v", decision, err)
	}
	if reason, allowed := ValidateExecutiveScope("deepseek", "http_adapter", decision.Classifications, ""); allowed || reason != "executive_scope_required" {
		t.Fatalf("policy allow bypassed scope gate: allowed=%v reason=%q", allowed, reason)
	}
}
