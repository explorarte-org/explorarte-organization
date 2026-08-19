package modelegress

import "testing"

// adversarialPolicy mirrors the canonical xai surface: public and sanitized
// only. organizational is absent on purpose -- there is no allow rule to find,
// so the Evaluator denies it as an undeclared combination rather than as a
// special case anyone had to remember to write.
func adversarialPolicy() ResolvedPolicy {
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rules := []Rule{
		{ProviderID: "*", DataClassification: ClassificationClinical, Effect: EffectDeny, ReasonCode: "clinical_egress_forbidden", HardDeny: true},
		{ProviderID: "*", DataClassification: ClassificationSecret, Effect: EffectDeny, ReasonCode: "secret_egress_forbidden", HardDeny: true},
		{ProviderID: "xai", DataClassification: ClassificationPublic, Effect: EffectAllow, ReasonCode: "public_egress_approved_for_xai_v1"},
		{ProviderID: "xai", DataClassification: ClassificationSanitized, Effect: EffectAllow, ReasonCode: "sanitized_egress_approved_for_xai_v1"},
	}
	return ResolvedPolicy{
		Version:                PolicyVersion{ID: 1, PolicyVersion: 7, CanonicalHash: hash, Status: "materialized"},
		OrganizationRevisionID: 7, CanonicalHash: hash, DefaultAction: EffectDeny, Rules: rules,
	}
}

func TestAdversarialReviewScopeMarkerBindsToTheReviewerRoleAlone(t *testing.T) {
	got := ExecutiveScopeMarker(AdversarialReviewerRoleID, "adversarial_review", "executive:abc", "task:40")
	if got != ScopeAdversarialReview {
		t.Fatalf("reviewer scope=%q, want %q", got, ScopeAdversarialReview)
	}
	// Every one of these is a role that could plausibly be pointed at the
	// review purpose by a future caller. None of them may earn the scope: the
	// designing department least of all, because a design reviewed by its own
	// author is the failure this whole role exists to prevent.
	for name, actor := range map[string]string{
		"design author":      "ingenieria_ia/orquestador",
		"design author peer": "ingenieria_ia/arquitecto_software",
		"ceo":                "empresa/ceo",
		"owner":              "empresa/human",
		"observer":           "empresa/ceo_observer",
		"sibling auditor":    "investigacion/auditor_cerebro_empresa",
	} {
		if got := ExecutiveScopeMarker(actor, "adversarial_review", "executive:abc", "task:40"); got != "" {
			t.Fatalf("%s unexpectedly earned adversarial scope: %q", name, got)
		}
	}
	// The reviewer role does not earn OTHER scopes either: it is not a
	// department leader or worker just because its id has two segments.
	for _, purpose := range []string{"department_plan", "department_review", "department_worker"} {
		got := ExecutiveScopeMarker(AdversarialReviewerRoleID, purpose, "executive:abc", "task:40")
		if got == ScopeAdversarialReview {
			t.Fatalf("purpose %q produced adversarial scope", purpose)
		}
	}
	// Durable executive metadata is still required.
	if got := ExecutiveScopeMarker(AdversarialReviewerRoleID, "adversarial_review", "other:abc", "task:40"); got != "" {
		t.Fatalf("non-executive correlation scoped as %q", got)
	}
	if got := ExecutiveScopeMarker(AdversarialReviewerRoleID, "adversarial_review", "executive:abc", ""); got != "" {
		t.Fatalf("missing task ref scoped as %q", got)
	}
}

func TestValidateExecutiveScopeForXAI(t *testing.T) {
	cases := []struct {
		name               string
		provider           string
		transport          string
		classes            []string
		scope              string
		singleProviderTest bool
		allowed            bool
		reason             string
	}{
		{"sanitized bundle with reviewer scope", "xai", "http_adapter", []string{"sanitized"}, ScopeAdversarialReview, false, true, "executive_scope_verified_adversarial_review"},
		{"public bundle with reviewer scope", "xai", "http_adapter", []string{"public"}, ScopeAdversarialReview, false, true, "executive_scope_verified_adversarial_review"},
		{"mixed sanitized and public", "xai", "http_adapter", []string{"public", "sanitized"}, ScopeAdversarialReview, false, true, "executive_scope_verified_adversarial_review"},

		// Scope is required for xai unconditionally -- including for public
		// data, which is the case a "harmless default" would have let through.
		{"public without scope", "xai", "http_adapter", []string{"public"}, "", false, false, "executive_scope_required"},
		{"sanitized without scope", "xai", "http_adapter", []string{"sanitized"}, "", false, false, "executive_scope_required"},

		// Borrowing another execution's scope does not work.
		{"ceo scope rejected", "xai", "http_adapter", []string{"sanitized"}, ScopeExecutiveCEO, false, false, "executive_scope_required"},
		{"leader scope rejected", "xai", "http_adapter", []string{"sanitized"}, ScopeDepartmentLeader, false, false, "executive_scope_required"},
		{"worker scope rejected", "xai", "http_adapter", []string{"sanitized"}, ScopeDepartmentWorker, false, false, "executive_scope_required"},

		{"non http transport rejected", "xai", "cli_adapter", []string{"sanitized"}, ScopeAdversarialReview, false, false, "executive_scope_required"},
		// singleProviderTest widens openai_compatible; it must not widen xai.
		{"single provider test does not widen xai", "xai", "http_adapter", []string{"sanitized"}, ScopeDepartmentWorker, true, false, "executive_scope_required"},

		// The mirror property: the reviewer's scope may not be spent on some
		// other provider. This is what makes a silent fallback to DeepSeek
		// impossible at the egress boundary rather than by convention.
		{"deepseek cannot serve reviewer scope", "deepseek", "http_adapter", []string{"sanitized"}, ScopeAdversarialReview, false, false, "executive_scope_required"},
		{"openai_compatible cannot serve reviewer scope", "openai_compatible", "http_adapter", []string{"organizational"}, ScopeAdversarialReview, false, false, "executive_scope_required"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reason, allowed := ValidateExecutiveScope(test.provider, test.transport, test.classes, test.scope, test.singleProviderTest)
			if allowed != test.allowed || reason != test.reason {
				t.Fatalf("got (%q,%v), want (%q,%v)", reason, allowed, test.reason, test.allowed)
			}
		})
	}
}

func TestAdversarialReviewEgressClassificationBoundary(t *testing.T) {
	policy := adversarialPolicy()
	evaluator := NewEvaluator()
	cases := []struct {
		name    string
		classes []string
		effect  Effect
		reason  string
	}{
		{"sanitized bundle", []string{"sanitized"}, EffectAllow, "sanitized_egress_approved_for_xai_v1"},
		{"public bundle", []string{"public"}, EffectAllow, "public_egress_approved_for_xai_v1"},
		{"clinical", []string{"clinical"}, EffectDeny, "clinical_egress_forbidden"},
		{"secret", []string{"secret"}, EffectDeny, "secret_egress_forbidden"},
		{"raw organizational", []string{"organizational"}, EffectDeny, "egress_combination_undeclared"},
		{"unknown", []string{"unknown"}, EffectDeny, "unknown_classification"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			decision, err := evaluator.Evaluate(EvaluationRequest{
				OrganizationRevisionID: 7, ProviderID: "xai", ProviderTransport: "http_adapter",
				ContextClassifications: test.classes, Policy: policy,
			})
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if decision.Effect != test.effect {
				t.Fatalf("effect=%q want %q (reasons=%v)", decision.Effect, test.effect, decision.ReasonCodes)
			}
			if len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != test.reason {
				t.Fatalf("reasons=%v want [%s]", decision.ReasonCodes, test.reason)
			}
		})
	}
	// A sanitized bundle that carries one contaminated segment is denied as a
	// whole. Classification is per-snapshot, so a single clinical segment
	// cannot be laundered by the presence of legitimate ones.
	decision, err := evaluator.Evaluate(EvaluationRequest{
		OrganizationRevisionID: 7, ProviderID: "xai", ProviderTransport: "http_adapter",
		ContextClassifications: []string{"sanitized", "public", "clinical"}, Policy: policy,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if decision.Effect != EffectDeny {
		t.Fatalf("contaminated bundle allowed: %+v", decision)
	}
	decision, err = evaluator.Evaluate(EvaluationRequest{
		OrganizationRevisionID: 7, ProviderID: "xai", ProviderTransport: "http_adapter",
		ContextClassifications: []string{"sanitized", "organizational"}, Policy: policy,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if decision.Effect != EffectDeny {
		t.Fatalf("organizational smuggled beside sanitized: %+v", decision)
	}
}
