package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
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

func validAuthorizationRequest() rag.AuthorizationRequest {
	return rag.AuthorizationRequest{
		OrganizationID: "explorarte", ActorRoleID: "investigacion/research_worker_hourly", CapabilityID: rag.CapabilityPropose,
		ResourceType: "knowledge_document", ResourceID: "gestion-riesgos-modelos", ActionDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
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
	gate, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectDeny, ReasonCode: "denied"}}, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Authorize(context.Background(), validAuthorizationRequest()); !errors.Is(err, authorization.ErrCapabilityDenied) {
		t.Fatalf("deny err=%v", err)
	}
	gate2, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectApprovalRequired, ReasonCode: "needs_owner"}}, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if err := gate2.Authorize(context.Background(), validAuthorizationRequest()); !errors.Is(err, authorization.ErrApprovalRequired) {
		t.Fatalf("approval required err=%v", err)
	}
}

func TestGateRejectsOrganizationMismatchAndMissingRevision(t *testing.T) {
	gate, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow}}, fakeRevisions{revision: &registry.Revision{ID: 1}}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	request := validAuthorizationRequest()
	request.OrganizationID = "other-org"
	if err := gate.Authorize(context.Background(), request); !errors.Is(err, authorization.ErrCapabilityDenied) {
		t.Fatalf("organization mismatch err=%v", err)
	}
	gateNoRevision, err := New(&fakeEvaluator{result: authorization.Evaluation{Effect: authorization.EffectAllow}}, fakeRevisions{revision: nil}, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	if err := gateNoRevision.Authorize(context.Background(), validAuthorizationRequest()); !errors.Is(err, authorization.ErrPolicyRevisionMismatch) {
		t.Fatalf("missing revision err=%v", err)
	}
}
