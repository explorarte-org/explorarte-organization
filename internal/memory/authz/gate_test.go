package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
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

func validAuthorizationRequest() memory.AuthorizationRequest {
	return memory.AuthorizationRequest{
		OrganizationID: "explorarte",
		ActorRoleID:    "ingenieria_ia/orquestador",
		CapabilityID:   memory.CapabilityPropose,
		ResourceType:   "organizational_memory",
		ResourceID:     "ingenieria_ia/orquestador",
		ActionDigest:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
}

func TestGateUsesCurrentRevisionAndExactActionScope(t *testing.T) {
	evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow, ReasonCode: authorization.ReasonAllowedByGrant}}
	gate, err := New(evaluator, fakeRevisions{revision: &registry.Revision{ID: 17}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	request := validAuthorizationRequest()
	if err := gate.Authorize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := evaluator.request
	if got.OrganizationRevisionID != 17 || got.OrganizationID != request.OrganizationID || got.ActorRoleID != request.ActorRoleID || got.CapabilityID != request.CapabilityID || got.ResourceType != request.ResourceType || got.ResourceID != request.ResourceID || got.ActionDigest != request.ActionDigest {
		t.Fatalf("authorization request drifted: %+v", got)
	}
}

func TestGateFailsClosedOnDenyAndApprovalRequired(t *testing.T) {
	for _, test := range []struct {
		name   string
		effect authorization.Effect
		want   error
	}{
		{name: "deny", effect: authorization.EffectDeny, want: authorization.ErrCapabilityDenied},
		{name: "approval", effect: authorization.EffectApprovalRequired, want: authorization.ErrApprovalRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: test.effect, ReasonCode: authorization.ReasonGrantMissing}}
			gate, err := New(evaluator, fakeRevisions{revision: &registry.Revision{ID: 17}}, "explorarte")
			if err != nil {
				t.Fatal(err)
			}
			if err := gate.Authorize(context.Background(), validAuthorizationRequest()); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
}

func TestGateRejectsOrganizationMismatchBeforeEvaluation(t *testing.T) {
	evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow}}
	gate, err := New(evaluator, fakeRevisions{revision: &registry.Revision{ID: 17}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	request := validAuthorizationRequest()
	request.OrganizationID = "other"
	if err := gate.Authorize(context.Background(), request); !errors.Is(err, authorization.ErrCapabilityDenied) {
		t.Fatalf("error=%v want capability denied", err)
	}
	if evaluator.request.OrganizationID != "" {
		t.Fatalf("organization mismatch reached evaluator: %+v", evaluator.request)
	}
}

func TestGateRejectsMissingActiveRevision(t *testing.T) {
	evaluator := &fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow}}
	gate, err := New(evaluator, fakeRevisions{}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Authorize(context.Background(), validAuthorizationRequest()); !errors.Is(err, authorization.ErrPolicyRevisionMismatch) {
		t.Fatalf("error=%v want policy revision mismatch", err)
	}
}
