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
		"no task ref": ExecutiveScopeMarker("empresa/ceo", "executive_ceo_plan", "executive:abc", ""),
		"owner cannot be worker": ExecutiveScopeMarker("empresa/human", "department_worker", "executive:abc", "task:10"),
		"observer cannot be leader": ExecutiveScopeMarker("empresa/ceo_observer", "department_review", "executive:abc", "task:10"),
		"unknown purpose": ExecutiveScopeMarker("ingenieria_ia/qa", "anything", "executive:abc", "task:10"),
	} {
		if got != "" {
			t.Fatalf("%s unexpectedly scoped as %q", name, got)
		}
	}
}

func TestScopedEgressCEOAlibaba(t *testing.T) {
	e := NewEvaluator()
	policy := r24Policy()
	base := EvaluationRequest{
		ProviderID: "alibaba_token_plan_via_claude_code", ProviderTransport: "cli_adapter",
		OrganizationRevisionID: 7, Policy: policy,
		ContextClassifications: []string{"organizational", "public"},
	}
	decision, err := e.Evaluate(base)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectDeny || !containsReason(decision.ReasonCodes, "executive_scope_required") {
		t.Fatalf("unscoped CEO decision=%+v", decision)
	}
	base.ContextClassifications = append(base.ContextClassifications, ScopeExecutiveCEO)
	decision, err = e.Evaluate(base)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectAllow || !containsReason(decision.ReasonCodes, "executive_scope_verified") {
		t.Fatalf("scoped CEO decision=%+v", decision)
	}
	base.ProviderTransport = "http_adapter"
	decision, _ = e.Evaluate(base)
	if decision.Effect != EffectDeny {
		t.Fatalf("wrong transport unexpectedly allowed: %+v", decision)
	}
}

func TestScopedEgressLeaderOpenAIAndPublicCompatibility(t *testing.T) {
	e := NewEvaluator()
	policy := r24Policy()
	organizational := EvaluationRequest{
		ProviderID: "openai_compatible", ProviderTransport: "http_adapter",
		OrganizationRevisionID: 7, Policy: policy,
		ContextClassifications: []string{"organizational", ScopeDepartmentLeader},
	}
	decision, err := e.Evaluate(organizational)
	if err != nil || decision.Effect != EffectAllow {
		t.Fatalf("leader decision=%+v err=%v", decision, err)
	}
	organizational.ContextClassifications = []string{"organizational", ScopeDepartmentWorker}
	decision, _ = e.Evaluate(organizational)
	if decision.Effect != EffectDeny {
		t.Fatalf("worker scope used for leader unexpectedly allowed: %+v", decision)
	}
	publicOnly := organizational
	publicOnly.ContextClassifications = []string{"public"}
	decision, err = e.Evaluate(publicOnly)
	if err != nil || decision.Effect != EffectAllow {
		t.Fatalf("legacy public OpenAI egress regressed: %+v err=%v", decision, err)
	}
}

func TestScopedEgressWorkerDeepSeekAndHardDenies(t *testing.T) {
	e := NewEvaluator()
	policy := r24Policy()
	request := EvaluationRequest{
		ProviderID: "deepseek", ProviderTransport: "http_adapter",
		OrganizationRevisionID: 7, Policy: policy,
		ContextClassifications: []string{"organizational", "sanitized", ScopeDepartmentWorker},
	}
	decision, err := e.Evaluate(request)
	if err != nil || decision.Effect != EffectAllow {
		t.Fatalf("worker decision=%+v err=%v", decision, err)
	}
	request.ContextClassifications = append(request.ContextClassifications, "secret")
	decision, err = e.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectDeny || !containsReason(decision.ReasonCodes, "secret_egress_forbidden") {
		t.Fatalf("hard deny was bypassed by scope: %+v", decision)
	}
}

func TestUnknownOrMultipleScopeFailsClosed(t *testing.T) {
	e := NewEvaluator()
	policy := r24Policy()
	for name, classes := range map[string][]string{
		"unknown": {"organizational", "scope.executive.made_up"},
		"multiple": {"organizational", ScopeExecutiveCEO, ScopeDepartmentWorker},
	} {
		t.Run(name, func(t *testing.T) {
			decision, err := e.Evaluate(EvaluationRequest{
				ProviderID: "alibaba_token_plan_via_claude_code", ProviderTransport: "cli_adapter",
				OrganizationRevisionID: 7, Policy: policy, ContextClassifications: classes,
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Effect != EffectDeny {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func containsReason(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
