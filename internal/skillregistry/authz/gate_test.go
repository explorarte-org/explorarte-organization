package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

type fakeEvaluator struct {
	result  authorization.Evaluation
	err     error
	request authorization.EvaluationRequest
}

func (f *fakeEvaluator) Evaluate(_ context.Context, request authorization.EvaluationRequest) (authorization.Evaluation, error) {
	f.request = request
	return f.result, f.err
}

type fakeRevisions struct {
	revision *registry.Revision
	err      error
}

func (f fakeRevisions) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	return f.revision, f.err
}

func TestAuthorizeProposalUsesProposeCapability(t *testing.T) {
	evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow, ReasonCode: authorization.ReasonAllowedByGrant}}
	gate, err := New(evaluator, fakeRevisions{revision: &registry.Revision{ID: 9}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := gate.AuthorizeProposal(context.Background(), "explorarte", "recursos_agenticos/disenador_skills", "auditar-ux")
	if err != nil {
		t.Fatal(err)
	}
	if evaluator.request.CapabilityID != skillregistry.CapabilityPropose || evaluator.request.OrganizationRevisionID != 9 {
		t.Fatalf("evaluation request drifted: %+v", evaluator.request)
	}
	if evidence.DecisionRef == "" || evidence.ActorRoleID != "recursos_agenticos/disenador_skills" {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestAuthorizeLifecycleChangeRoutesActivationToActivateCapability(t *testing.T) {
	cases := []struct {
		from, to   skillregistry.Lifecycle
		capability string
	}{
		{skillregistry.LifecycleDraft, skillregistry.LifecycleHumanApproved, skillregistry.CapabilityPropose},
		{skillregistry.LifecycleHumanApproved, skillregistry.LifecycleCandidate, skillregistry.CapabilityPropose},
		{skillregistry.LifecycleCandidate, skillregistry.LifecycleActive, skillregistry.CapabilityActivate},
		{skillregistry.LifecycleActive, skillregistry.LifecycleSuspended, skillregistry.CapabilityActivate},
		{skillregistry.LifecycleSuspended, skillregistry.LifecycleActive, skillregistry.CapabilityActivate},
		{skillregistry.LifecycleActive, skillregistry.LifecycleRetired, skillregistry.CapabilityActivate},
	}
	for _, tc := range cases {
		evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow, ReasonCode: authorization.ReasonAllowedByGrant}}
		gate, err := New(evaluator, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gate.AuthorizeLifecycleChange(context.Background(), "explorarte", "empresa/human", "auditar-ux", tc.from, tc.to); err != nil {
			t.Fatal(err)
		}
		if evaluator.request.CapabilityID != tc.capability {
			t.Fatalf("%s->%s used capability %q, want %q", tc.from, tc.to, evaluator.request.CapabilityID, tc.capability)
		}
	}
}

func TestAuthorizeAssignmentChangeUsesActivateCapability(t *testing.T) {
	evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow, ReasonCode: authorization.ReasonAllowedByGrant}}
	gate, err := New(evaluator, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.AuthorizeAssignmentChange(context.Background(), "explorarte", "empresa/human", "ingenieria_ia/frontend", "auditar-ux", "assign"); err != nil {
		t.Fatal(err)
	}
	if evaluator.request.CapabilityID != skillregistry.CapabilityActivate || evaluator.request.ResourceType != "skill_assignment" {
		t.Fatalf("evaluation request drifted: %+v", evaluator.request)
	}
}

func TestGateFailsClosedOnDenyAndApprovalRequired(t *testing.T) {
	gate, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectDeny, ReasonCode: "denied"}}, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.AuthorizeProposal(context.Background(), "explorarte", "empresa/human", "auditar-ux"); !errors.Is(err, authorization.ErrCapabilityDenied) {
		t.Fatalf("deny err=%v", err)
	}
	gate2, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectApprovalRequired, ReasonCode: "needs_owner"}}, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate2.AuthorizeProposal(context.Background(), "explorarte", "empresa/human", "auditar-ux"); !errors.Is(err, authorization.ErrApprovalRequired) {
		t.Fatalf("approval required err=%v", err)
	}
}

func TestGateRejectsOrganizationMismatchAndMissingRevision(t *testing.T) {
	gate, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow}}, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.AuthorizeProposal(context.Background(), "other-org", "empresa/human", "auditar-ux"); !errors.Is(err, authorization.ErrCapabilityDenied) {
		t.Fatalf("organization mismatch err=%v", err)
	}
	gateNoRevision, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow}}, fakeRevisions{revision: nil}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateNoRevision.AuthorizeProposal(context.Background(), "explorarte", "empresa/human", "auditar-ux"); !errors.Is(err, authorization.ErrPolicyRevisionMismatch) {
		t.Fatalf("missing revision err=%v", err)
	}
}
