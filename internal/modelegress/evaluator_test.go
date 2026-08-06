package modelegress

import "testing"

func fixtureResolvedPolicy() ResolvedPolicy {
	return ResolvedPolicy{
		Version:                PolicyVersion{ID: 1, PolicyVersion: 1, Status: "materialized", CanonicalHash: SHA256Bytes([]byte("fixture"))},
		OrganizationRevisionID: 7,
		CanonicalHash:          SHA256Bytes([]byte("fixture")),
		DefaultAction:          EffectDeny,
		Rules: []Rule{
			{ProviderID: "*", DataClassification: ClassificationSecret, Effect: EffectDeny, ReasonCode: "secret_egress_forbidden", HardDeny: true},
			{ProviderID: "*", DataClassification: ClassificationClinical, Effect: EffectDeny, ReasonCode: "clinical_egress_forbidden", HardDeny: true},
			{ProviderID: "test.fake", DataClassification: ClassificationPublic, Effect: EffectAllow, ReasonCode: "fixture_public_allow"},
			{ProviderID: "test.fake", DataClassification: ClassificationSanitized, Effect: EffectAllow, ReasonCode: "fixture_sanitized_allow"},
			{ProviderID: "test.fake", DataClassification: ClassificationOrganizational, Effect: EffectAllow, ReasonCode: "fixture_organizational_allow"},
		},
	}
}

func TestEvaluatorFixtureAllowsDeclaredClasses(t *testing.T) {
	evaluator := NewEvaluator()
	for _, classification := range []string{"public", "sanitized", "organizational"} {
		decision, err := evaluator.Evaluate(EvaluationRequest{ProviderID: "test.fake", ProviderTransport: "fake_adapter", OrganizationRevisionID: 7, Policy: fixtureResolvedPolicy(), ContextClassifications: []string{classification}})
		if err != nil {
			t.Fatalf("%s: %v", classification, err)
		}
		wantReason := "fixture_" + classification + "_allow"
		if decision.Effect != EffectAllow || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != wantReason {
			t.Fatalf("%s: %#v", classification, decision)
		}
	}
}

func TestEvaluatorRejectsMalformedResolvedPolicy(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ResolvedPolicy)
	}{
		{name: "zero semantic version", mutate: func(policy *ResolvedPolicy) { policy.Version.PolicyVersion = 0 }},
		{name: "allow default", mutate: func(policy *ResolvedPolicy) { policy.DefaultAction = EffectAllow }},
		{name: "invalid hash", mutate: func(policy *ResolvedPolicy) { policy.CanonicalHash = "invalid" }},
		{name: "version hash mismatch", mutate: func(policy *ResolvedPolicy) { policy.Version.CanonicalHash = SHA256Bytes([]byte("other")) }},
		{name: "revision mismatch", mutate: func(policy *ResolvedPolicy) { policy.OrganizationRevisionID++ }},
		{name: "not materialized", mutate: func(policy *ResolvedPolicy) { policy.Version.Status = "materializing" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			policy := fixtureResolvedPolicy()
			policy.Version.PolicyVersion = 1
			test.mutate(&policy)
			decision, err := NewEvaluator().Evaluate(EvaluationRequest{ProviderID: "test.fake", ProviderTransport: "fake_adapter", OrganizationRevisionID: 7, Policy: policy, ContextClassifications: []string{"public"}})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Effect != EffectDeny || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "egress_policy_unresolved" {
				t.Fatalf("decision=%#v", decision)
			}
		})
	}
}

func TestEvaluatorDefaultAndHardDenies(t *testing.T) {
	evaluator := NewEvaluator()
	cases := []struct {
		name       string
		provider   string
		classes    []string
		reasonCode string
	}{
		{name: "empty", provider: "test.fake", classes: nil, reasonCode: "empty_classification_set"},
		{name: "unknown", provider: "test.fake", classes: []string{"unknown"}, reasonCode: "unknown_classification"},
		{name: "secret", provider: "test.fake", classes: []string{"secret"}, reasonCode: "secret_egress_forbidden"},
		{name: "clinical", provider: "test.fake", classes: []string{"clinical"}, reasonCode: "clinical_egress_forbidden"},
		{name: "mixed hard deny", provider: "test.fake", classes: []string{"public", "clinical"}, reasonCode: "clinical_egress_forbidden"},
		{name: "undeclared provider", provider: "missing", classes: []string{"public"}, reasonCode: "provider_undeclared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := evaluator.Evaluate(EvaluationRequest{ProviderID: tc.provider, ProviderTransport: "fake_adapter", OrganizationRevisionID: 7, Policy: fixtureResolvedPolicy(), ContextClassifications: tc.classes})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Effect != EffectDeny || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != tc.reasonCode {
				t.Fatalf("decision=%#v", decision)
			}
		})
	}
}

func TestEvaluatorUndeclaredCombinationUsesDefaultDeny(t *testing.T) {
	policy := fixtureResolvedPolicy()
	policy.Rules = policy.Rules[:2]
	decision, err := NewEvaluator().Evaluate(EvaluationRequest{ProviderID: "test.fake", ProviderTransport: "fake_adapter", OrganizationRevisionID: 7, Policy: policy, ContextClassifications: []string{"public"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectDeny || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "provider_undeclared" {
		t.Fatalf("decision=%#v", decision)
	}

	policy.Rules = append(policy.Rules, Rule{ProviderID: "test.fake", DataClassification: ClassificationOrganizational, Effect: EffectAllow, ReasonCode: "fixture_organizational_allow"})
	decision, err = NewEvaluator().Evaluate(EvaluationRequest{ProviderID: "test.fake", ProviderTransport: "fake_adapter", OrganizationRevisionID: 7, Policy: policy, ContextClassifications: []string{"public"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectDeny || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "egress_combination_undeclared" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestClassificationAndDecisionHashesAreStable(t *testing.T) {
	first, firstHash := NormalizeClassifications([]string{"organizational", "public", "public"})
	second, secondHash := NormalizeClassifications([]string{"public", "organizational"})
	if firstHash != secondHash || len(first) != 2 || len(second) != 2 {
		t.Fatalf("normalization mismatch: %v %s / %v %s", first, firstHash, second, secondHash)
	}
	evaluation := PreSendEvaluation{InvocationID: 1, DispatchAttemptID: 2, OrganizationID: "explorarte", OrganizationRevisionID: 7, ModelProfileVersionID: 8, ProviderID: "test.fake", ProviderTransport: "fake_adapter", PolicyVersionID: 9, PolicyHash: SHA256Bytes([]byte("policy")), ActionDigest: SHA256Bytes([]byte("action")), CapabilityMatrixHash: SHA256Bytes([]byte("matrix")), ContextClassifications: []string{"public", "organizational", "public"}, ContextClassificationsHash: firstHash, AuthorizationEffect: AuthorizationAllow, AuthorizationReasonCode: "allowed_by_grant", EgressEffect: EffectAllow, EgressReasonCodes: []string{"b", "a", "a"}}
	firstDecision := DecisionHash(evaluation)
	evaluation.ContextClassifications = []string{"organizational", "public"}
	evaluation.EgressReasonCodes = []string{"a", "b"}
	if secondDecision := DecisionHash(evaluation); firstDecision != secondDecision {
		t.Fatalf("decision hash changed: %s != %s", firstDecision, secondDecision)
	}
}

func TestInvocationActionDigestPinsImmutableScope(t *testing.T) {
	requestHash := SHA256Bytes([]byte("request"))
	policyHash := SHA256Bytes([]byte("policy"))
	base, err := InvocationActionDigest(11, requestHash, 17, policyHash)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		invocationID int64
		requestHash  string
		policyID     int64
		policyHash   string
	}{
		{12, requestHash, 17, policyHash},
		{11, SHA256Bytes([]byte("other-request")), 17, policyHash},
		{11, requestHash, 18, policyHash},
		{11, requestHash, 17, SHA256Bytes([]byte("other-policy"))},
	}
	for index, candidate := range cases {
		digest, digestErr := InvocationActionDigest(candidate.invocationID, candidate.requestHash, candidate.policyID, candidate.policyHash)
		if digestErr != nil {
			t.Fatalf("case %d: %v", index, digestErr)
		}
		if digest == base {
			t.Fatalf("case %d did not change action digest", index)
		}
	}
	if _, err = InvocationActionDigest(0, requestHash, 17, policyHash); err == nil {
		t.Fatal("invalid action scope was accepted")
	}
}

func productiveResolvedPolicy() ResolvedPolicy {
	policy := fixtureResolvedPolicy()
	policy.Rules = []Rule{
		{ProviderID: "*", DataClassification: ClassificationSecret, Effect: EffectDeny, ReasonCode: "secret_egress_forbidden", HardDeny: true},
		{ProviderID: "*", DataClassification: ClassificationClinical, Effect: EffectDeny, ReasonCode: "clinical_egress_forbidden", HardDeny: true},
		{ProviderID: "deepseek", DataClassification: ClassificationPublic, Effect: EffectDeny, ReasonCode: "public_egress_not_approved"},
		{ProviderID: "deepseek", DataClassification: ClassificationSanitized, Effect: EffectDeny, ReasonCode: "sanitized_egress_not_approved"},
		{ProviderID: "deepseek", DataClassification: ClassificationOrganizational, Effect: EffectDeny, ReasonCode: "organizational_egress_not_approved"},
	}
	return policy
}

func TestEvaluatorProductivePolicyDeniesEveryClassification(t *testing.T) {
	evaluator := NewEvaluator()
	for _, classifications := range [][]string{{"public"}, {"sanitized"}, {"organizational"}, {"secret"}, {"clinical"}, {"unknown"}, nil} {
		decision, err := evaluator.Evaluate(EvaluationRequest{ProviderID: "deepseek", ProviderTransport: "http_adapter", OrganizationRevisionID: 7, Policy: productiveResolvedPolicy(), ContextClassifications: classifications})
		if err != nil {
			t.Fatalf("classes=%v err=%v", classifications, err)
		}
		if decision.Effect != EffectDeny {
			t.Fatalf("classes=%v decision=%#v", classifications, decision)
		}
	}
}

func TestEvaluatorRejectsPolicyHashMismatch(t *testing.T) {
	policy := fixtureResolvedPolicy()
	policy.Version.CanonicalHash = SHA256Bytes([]byte("different"))
	decision, err := NewEvaluator().Evaluate(EvaluationRequest{ProviderID: "test.fake", ProviderTransport: "fake_adapter", OrganizationRevisionID: 7, Policy: policy, ContextClassifications: []string{"public"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectDeny || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "egress_policy_unresolved" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestEvaluatorHardDenyDominatesUnknownProvider(t *testing.T) {
	decision, err := NewEvaluator().Evaluate(EvaluationRequest{
		ProviderID: "missing", ProviderTransport: "http_adapter", OrganizationRevisionID: 7,
		Policy: fixtureResolvedPolicy(), ContextClassifications: []string{"public", "clinical"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EffectDeny || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "clinical_egress_forbidden" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestEvaluatorRejectsInvalidTransportAsOperationalError(t *testing.T) {
	decision, err := NewEvaluator().Evaluate(EvaluationRequest{
		ProviderID: "test.fake", ProviderTransport: "shell", OrganizationRevisionID: 7,
		Policy: fixtureResolvedPolicy(), ContextClassifications: []string{"public"},
	})
	if err == nil || decision.Effect != EffectError || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "provider_transport_invalid" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestNormalizeClassificationsMapsUndeclaredValuesToUnknown(t *testing.T) {
	classes, hash := NormalizeClassifications([]string{"PII", "unknown", " public ", "PII"})
	want := []string{"public", "unknown"}
	if !equalStrings(classes, want) {
		t.Fatalf("classes=%v want=%v", classes, want)
	}
	_, wantHash := NormalizeClassifications(want)
	if hash != wantHash {
		t.Fatalf("hash=%s want=%s", hash, wantHash)
	}
}
