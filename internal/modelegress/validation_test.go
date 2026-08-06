package modelegress

import (
	"errors"
	"testing"
	"time"
)

func validPreSendEvaluation() PreSendEvaluation {
	classes, classHash := NormalizeClassifications([]string{"organizational", "public"})
	evaluation := PreSendEvaluation{
		InvocationID: 1, DispatchAttemptID: 2, PolicyVersionID: 3,
		PolicyHash: SHA256Bytes([]byte("policy")), OrganizationID: "explorarte",
		OrganizationRevisionID: 4, DispatchActorRoleID: "ingenieria_ia/code-runner",
		SubjectRoleID: "ingenieria_ia/code-runner", ModelProfileVersionID: 5,
		ProviderID: "test.fake", ProviderTransport: "fake_adapter",
		ActionDigest: SHA256Bytes([]byte("action")), CapabilityMatrixHash: SHA256Bytes([]byte("matrix")),
		ContextClassifications: classes, ContextClassificationsHash: classHash,
		AuthorizationEffect: AuthorizationAllow, AuthorizationReasonCode: "allowed_by_grant",
		EgressEffect: EffectAllow, EgressReasonCodes: []string{"fixture_allow"},
	}
	evaluation.DecisionHash = DecisionHash(evaluation)
	return evaluation
}

func TestValidatePreSendEvaluation(t *testing.T) {
	if err := ValidatePreSendEvaluation(validPreSendEvaluation()); err != nil {
		t.Fatal(err)
	}
	cases := []func(*PreSendEvaluation){
		func(value *PreSendEvaluation) { value.ContextClassifications = []string{"public", "organizational"} },
		func(value *PreSendEvaluation) { value.ContextClassificationsHash = SHA256Bytes([]byte("wrong")) },
		func(value *PreSendEvaluation) { value.AuthorizationEffect = AuthorizationDeny },
		func(value *PreSendEvaluation) { value.ProviderTransport = "shell" },
		func(value *PreSendEvaluation) { value.DecisionHash = SHA256Bytes([]byte("wrong")) },
	}
	for index, mutate := range cases {
		value := validPreSendEvaluation()
		mutate(&value)
		if err := ValidatePreSendEvaluation(value); err == nil {
			t.Fatalf("case %d unexpectedly passed", index)
		}
	}
}

func TestValidatePreSendAllowRejectsEmptyUnknownAndHardDeniedClassifications(t *testing.T) {
	for _, classes := range [][]string{nil, {"unknown"}, {"secret"}, {"clinical"}, {"public", "clinical"}} {
		value := validPreSendEvaluation()
		value.ContextClassifications, value.ContextClassificationsHash = NormalizeClassifications(classes)
		value.DecisionHash = DecisionHash(value)
		if err := ValidatePreSendEvaluation(value); err == nil {
			t.Fatalf("classes=%v unexpectedly accepted as allow", classes)
		}
	}
}

func TestValidatePersistCommands(t *testing.T) {
	value := validPreSendEvaluation()
	allow := PersistAllowCommand{Evaluation: value, ClaimToken: "claim", ProviderIdempotencyKeyHash: SHA256Bytes([]byte("provider")), Deadline: time.Now().Add(time.Hour)}
	if err := ValidatePersistAllowCommand(allow); err != nil {
		t.Fatal(err)
	}
	failure := PersistDenyCommand{Evaluation: value, ClaimToken: "claim", ErrorCode: "adapter_unavailable", OutboxMaxAttempts: 10}
	if err := ValidatePersistFailureCommand(failure); err == nil {
		t.Fatal("allow decision was accepted by the failure transition")
	}

	deny := validPreSendEvaluation()
	deny.AuthorizationEffect = AuthorizationDeny
	deny.AuthorizationReasonCode = "grant_missing"
	deny.EgressEffect = EffectNotEvaluated
	deny.EgressReasonCodes = nil
	deny.DecisionHash = DecisionHash(deny)
	if err := ValidatePersistFailureCommand(PersistDenyCommand{Evaluation: deny, ClaimToken: "claim", ErrorCode: "authorization_denied", OutboxMaxAttempts: 10}); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePersistAllowCommand(PersistAllowCommand{Evaluation: deny, ClaimToken: "claim", ProviderIdempotencyKeyHash: SHA256Bytes([]byte("provider")), Deadline: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("authorization deny was accepted as pre-send allow")
	}
}

func TestValidateRegistryPlan(t *testing.T) {
	policy := CanonicalPolicy{
		PolicyID: "model-egress", PolicyVersion: 1, DefaultAction: EffectDeny,
		CanonicalHash: SHA256Bytes([]byte("policy")),
		HardDenies: []HardDeny{
			{DataClassification: ClassificationClinical, ReasonCode: "clinical_egress_forbidden"},
			{DataClassification: ClassificationSecret, ReasonCode: "secret_egress_forbidden"},
		},
		Rules: []Rule{{ProviderID: "test.fake", DataClassification: ClassificationOrganizational, Effect: EffectAllow, ReasonCode: "fixture_allow"}},
	}
	valid := RegistryPlan{OrganizationID: "explorarte", OrganizationRevisionID: 7, CanonicalHash: policy.CanonicalHash, Policy: policy}
	if err := ValidateRegistryPlan(valid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*RegistryPlan)
	}{
		{name: "hash mismatch", mutate: func(plan *RegistryPlan) { plan.Policy.CanonicalHash = SHA256Bytes([]byte("other")) }},
		{name: "missing hard deny", mutate: func(plan *RegistryPlan) { plan.Policy.HardDenies = plan.Policy.HardDenies[:1] }},
		{name: "duplicate rule", mutate: func(plan *RegistryPlan) { plan.Policy.Rules = append(plan.Policy.Rules, plan.Policy.Rules[0]) }},
		{name: "invalid provider", mutate: func(plan *RegistryPlan) { plan.Policy.Rules[0].ProviderID = "BAD PROVIDER" }},
		{name: "hard deny provider rule", mutate: func(plan *RegistryPlan) { plan.Policy.Rules[0].HardDeny = true }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Policy.HardDenies = append([]HardDeny(nil), valid.Policy.HardDenies...)
			candidate.Policy.Rules = append([]Rule(nil), valid.Policy.Rules...)
			test.mutate(&candidate)
			if err := ValidateRegistryPlan(candidate); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
