package search

import (
	"context"
	"testing"
)

func TestPolicy_CEO_CanWebNewsAcademicBookGeneral(t *testing.T) {
	policies := DefaultRolePolicies()
	policy, ok := policies[RoleCEO]
	if !ok {
		t.Fatal("CEO policy not found")
	}

	intents := []SearchIntent{IntentWebGeneral, IntentNews, IntentAcademic, IntentBookGeneral}
	for _, intent := range intents {
		if !intentAllowed(intent, policy.AllowedIntents) {
			t.Errorf("CEO should allow %v", intent)
		}
	}
}

func TestPolicy_CEO_CannotAcademicOrBiomedicalOrPreprint(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleCEO]

	notAllowed := []SearchIntent{IntentBiomedical, IntentPreprint, IntentBookAcademicOA, IntentBookPublicDomain}
	for _, intent := range notAllowed {
		if intentAllowed(intent, policy.AllowedIntents) {
			t.Errorf("CEO should NOT allow %v", intent)
		}
	}
}

func TestPolicy_CEO_CannotSubmitRAGCandidate(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleCEO]
	if policy.CanSubmitRAGCandidate {
		t.Error("CEO should NOT be able to submit RAG candidate")
	}
}

func TestPolicy_CEO_CannotPromoteToKnowledge(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleCEO]
	if policy.CanPromoteToKnowledge {
		t.Error("CEO should NOT be able to promote to knowledge")
	}
}

func TestPolicy_Marketing_CannotAcademic(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleMarketing]
	if intentAllowed(IntentAcademic, policy.AllowedIntents) {
		t.Error("Marketing should NOT be able to use academic")
	}
}

func TestPolicy_Marketing_CannotSubmitRAGCandidate(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleMarketing]
	if policy.CanSubmitRAGCandidate {
		t.Error("Marketing should NOT be able to submit RAG candidate")
	}
}

func TestPolicy_Marketing_CannotPromoteToKnowledge(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleMarketing]
	if policy.CanPromoteToKnowledge {
		t.Error("Marketing should NOT be able to promote to knowledge")
	}
}

func TestPolicy_Research_CanAcademicBiomedicalPreprint(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleResearch]

	allowed := []SearchIntent{IntentAcademic, IntentBiomedical, IntentPreprint}
	for _, intent := range allowed {
		if !intentAllowed(intent, policy.AllowedIntents) {
			t.Errorf("Research should allow %v", intent)
		}
	}
}

func TestPolicy_Research_CanAllBookIntents(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleResearch]

	bookIntents := []SearchIntent{IntentBookGeneral, IntentBookAcademicOA, IntentBookPublicDomain}
	for _, intent := range bookIntents {
		if !intentAllowed(intent, policy.AllowedIntents) {
			t.Errorf("Research should allow %v", intent)
		}
	}
}

func TestPolicy_Research_CanSubmitRAGCandidate(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleResearch]
	if !policy.CanSubmitRAGCandidate {
		t.Error("Research should be able to submit RAG candidate")
	}
}

func TestPolicy_Research_CannotPromoteToKnowledge(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleResearch]
	if policy.CanPromoteToKnowledge {
		t.Error("Research should NOT be able to promote directly to Knowledge")
	}
}

func TestPolicy_Research_NoGoogleFallback(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleResearch]
	if policy.AllowGoogleFallback {
		t.Error("Research should NOT allow Google fallback")
	}
}

func TestPolicy_Adversarial_CannotSubmitRAGCandidate(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleAdversarial]
	if policy.CanSubmitRAGCandidate {
		t.Error("Adversarial should NOT be able to submit RAG candidate")
	}
}

func TestPolicy_Adversarial_CannotPromoteToKnowledge(t *testing.T) {
	policies := DefaultRolePolicies()
	policy := policies[RoleAdversarial]
	if policy.CanPromoteToKnowledge {
		t.Error("Adversarial should NOT be able to promote to knowledge")
	}
}

func TestRouter_UnauthorizedRole_ReturnsError(t *testing.T) {
	router, err := NewRouter(RouterConfig{
		Policies: DefaultRolePolicies(),
	})
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: "unknown_role",
	}

	_, err = router.Search(context.Background(), req)
	if err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestRouter_UnauthorizedIntent_ReturnsError(t *testing.T) {
	router, err := NewRouter(RouterConfig{
		Policies: DefaultRolePolicies(),
	})
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	// Marketing using Academic (not allowed)
	req := SearchRequest{
		Query:  "test",
		Intent: IntentAcademic,
		RoleID: string(RoleMarketing),
	}

	_, err = router.Search(context.Background(), req)
	if err == nil {
		t.Error("expected error for unauthorized intent")
	}
}
