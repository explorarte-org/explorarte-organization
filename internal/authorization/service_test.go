package authorization

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type fakeStore struct {
	organization  registry.Organization
	revision      *registry.Revision
	role          registry.Role
	request       ApprovalRequest
	createResult  RequestApprovalResult
	consumeResult ConsumeApprovalResult
	consumeErr    error
}

func (f *fakeStore) GetOrganization(context.Context, string) (registry.Organization, error) {
	return f.organization, nil
}
func (f *fakeStore) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	return f.revision, nil
}
func (f *fakeStore) GetAuthorizationRole(context.Context, string, string) (registry.Role, error) {
	return f.role, nil
}
func (f *fakeStore) CreateRequest(_ context.Context, request ApprovalRequest, _ int) (RequestApprovalResult, error) {
	f.request = request
	if f.createResult.Request.ID != 0 {
		return f.createResult, nil
	}
	request.ID = 1
	return RequestApprovalResult{Request: request}, nil
}
func (f *fakeStore) DecideRequest(context.Context, DecideRequestCommand, time.Time, DecisionValidator, int) (ApprovalRequest, error) {
	return f.request, nil
}
func (f *fakeStore) ConsumeApproval(_ context.Context, command ConsumeApprovalCommand, _ time.Time, validator ApprovalValidator, _ int) (ConsumeApprovalResult, error) {
	if f.consumeErr != nil {
		return f.consumeResult, f.consumeErr
	}
	if f.consumeResult.Request.ID != 0 {
		return f.consumeResult, nil
	}
	reason := validator(ApprovalValidationContext{Request: f.request, Organization: f.organization, Revision: *f.revision, Role: f.role}, command)
	if reason != "" {
		return ConsumeApprovalResult{Request: f.request, ReasonCode: reason}, nil
	}
	use := ApprovalUse{ID: 1, RequestID: f.request.ID, OrganizationID: f.request.OrganizationID, ConsumedByRoleID: command.ActorRoleID, ActionDigest: command.ActionDigest}
	f.request.Status = RequestConsumed
	return ConsumeApprovalResult{Request: f.request, Use: &use}, nil
}
func (f *fakeStore) CancelRequest(context.Context, CancelRequestCommand, time.Time, int) (ApprovalRequest, error) {
	return f.request, nil
}
func (f *fakeStore) ExpireRequests(context.Context, string, time.Time, int, int) (ExpireResult, error) {
	return ExpireResult{Expired: 1}, nil
}
func (f *fakeStore) GetRequest(context.Context, int64) (ApprovalRequest, error) {
	return f.request, nil
}
func (f *fakeStore) ListRequests(context.Context, ListRequestsFilter) ([]ApprovalRequest, error) {
	return []ApprovalRequest{f.request}, nil
}

func testService(t *testing.T) (*Service, *fakeStore, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	policy, policyReader := testAuthorizer(t)
	store := &fakeStore{organization: policyReader.organization, revision: policyReader.revision, role: policyReader.roles["empresa/human"]}
	service, err := NewService(ServiceConfig{OrganizationID: "explorarte", DefaultTTL: 30 * time.Minute, MaxTTL: 24 * time.Hour, ExpireBatchSize: 100, OutboxMaxAttempts: 10}, policy, store, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	return service, store, now
}

func TestRequestApprovalUsesDurableScopeAndTTL(t *testing.T) {
	service, store, now := testService(t)
	digest := DigestAction([]byte("activate skill x"))
	result, err := service.RequestApproval(context.Background(), RequestApprovalCommand{ActorRoleID: "empresa/human", CapabilityID: "organization.activate_skill", ResourceType: "skill", ResourceID: "x", ActionDigest: digest, IdempotencyKey: "request-1", Reason: "activate once"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.ExpiresAt != now.Add(30*time.Minute) || result.Request.RequestHash == "" || result.Request.ApprovalMode != "owner" {
		t.Fatalf("request=%+v", result.Request)
	}
	if store.request.CapabilityMatrixHash == "" {
		t.Fatal("matrix hash not persisted")
	}
}

func TestRequestApprovalRejectsNonApprovalAndInvalidTTL(t *testing.T) {
	service, _, _ := testService(t)
	base := RequestApprovalCommand{ActorRoleID: "empresa/human", CapabilityID: "code.commit", ResourceType: "code", ResourceID: "x", ActionDigest: DigestAction([]byte("x")), IdempotencyKey: "x", Reason: "x"}
	if _, err := service.RequestApproval(context.Background(), base); !errors.Is(err, ErrApprovalNotRequired) {
		t.Fatalf("got %v", err)
	}
	base.CapabilityID = "organization.activate_skill"
	base.TTL = 25 * time.Hour
	if _, err := service.RequestApproval(context.Background(), base); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v", err)
	}
}

func TestEvaluateConsumesValidApprovalBeforeAllow(t *testing.T) {
	service, store, now := testService(t)
	digest := DigestAction([]byte("activate skill x"))
	store.request = ApprovalRequest{ID: 9, OrganizationID: "explorarte", OrganizationRevisionID: 7, CapabilityMatrixHash: service.policy.MatrixHash(), RequesterRoleID: "empresa/human", CapabilityID: "organization.activate_skill", Risk: "high", ApprovalMode: "owner", ResourceType: "skill", ResourceID: "x", ActionDigest: digest, Status: RequestApproved, ExpiresAt: now.Add(time.Hour)}
	id := int64(9)
	got, err := service.Evaluate(context.Background(), EvaluationRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, ActorRoleID: "empresa/human", CapabilityID: "organization.activate_skill", ResourceType: "skill", ResourceID: "x", ActionDigest: digest, ApprovalRequestID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if got.Effect != EffectAllow || got.ReasonCode != ReasonAllowedByApproval || store.request.Status != RequestConsumed {
		t.Fatalf("evaluation=%+v request=%+v", got, store.request)
	}
}

func TestEvaluateApprovalStatesAndScope(t *testing.T) {
	service, store, now := testService(t)
	digest := DigestAction([]byte("activate skill x"))
	request := ApprovalRequest{ID: 9, OrganizationID: "explorarte", OrganizationRevisionID: 7, CapabilityMatrixHash: service.policy.MatrixHash(), RequesterRoleID: "empresa/human", CapabilityID: "organization.activate_skill", Risk: "high", ApprovalMode: "owner", ResourceType: "skill", ResourceID: "x", ActionDigest: digest, Status: RequestApproved, ExpiresAt: now.Add(time.Hour)}
	store.request = request
	for _, tc := range []struct {
		name   string
		reason ReasonCode
		effect Effect
	}{{"pending", ReasonApprovalPending, EffectApprovalRequired}, {"rejected", ReasonApprovalRejected, EffectDeny}, {"expired", ReasonApprovalExpired, EffectDeny}, {"consumed", ReasonApprovalConsumed, EffectDeny}} {
		t.Run(tc.name, func(t *testing.T) {
			store.consumeResult = ConsumeApprovalResult{Request: request, ReasonCode: tc.reason}
			id := int64(9)
			got, err := service.Evaluate(context.Background(), EvaluationRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, ActorRoleID: "empresa/human", CapabilityID: request.CapabilityID, ResourceType: request.ResourceType, ResourceID: request.ResourceID, ActionDigest: digest, ApprovalRequestID: &id})
			if err != nil {
				t.Fatal(err)
			}
			if got.Effect != tc.effect || got.ReasonCode != tc.reason {
				t.Fatalf("got %+v", got)
			}
		})
	}
	store.consumeResult = ConsumeApprovalResult{}
	result, err := service.ConsumeApproval(context.Background(), ConsumeApprovalCommand{RequestID: 9, ActorRoleID: "empresa/human", ActionDigest: DigestAction([]byte("different"))})
	if !errors.Is(err, ErrApprovalScopeMismatch) || result.ReasonCode != ReasonApprovalScopeMismatch {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestApprovalPolicyDriftAndHardDeny(t *testing.T) {
	service, store, now := testService(t)
	digest := DigestAction([]byte("activate skill x"))
	store.request = ApprovalRequest{ID: 9, OrganizationID: "explorarte", OrganizationRevisionID: 7, CapabilityMatrixHash: strings.Repeat("b", 64), RequesterRoleID: "empresa/human", CapabilityID: "organization.activate_skill", ApprovalMode: "owner", ResourceType: "skill", ResourceID: "x", ActionDigest: digest, Status: RequestApproved, ExpiresAt: now.Add(time.Hour)}
	result, err := service.ConsumeApproval(context.Background(), ConsumeApprovalCommand{RequestID: 9, ActorRoleID: "empresa/human", ActionDigest: digest})
	if !errors.Is(err, ErrApprovalPolicyDrift) || result.ReasonCode != ReasonApprovalPolicyDrift {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	store.request.CapabilityMatrixHash = service.policy.MatrixHash()
	store.request.CapabilityID = "cell.read_clinical_data"
	result, err = service.ConsumeApproval(context.Background(), ConsumeApprovalCommand{RequestID: 9, ActorRoleID: "empresa/human", ActionDigest: digest})
	if !errors.Is(err, ErrCapabilityDenied) || result.ReasonCode != ReasonHardDeny {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestEvaluateRejectsApprovalResourceScopeMismatch(t *testing.T) {
	service, store, now := testService(t)
	digest := DigestAction([]byte("activate skill x"))
	store.request = ApprovalRequest{ID: 10, OrganizationID: "explorarte", OrganizationRevisionID: 7, CapabilityMatrixHash: service.policy.MatrixHash(), RequesterRoleID: "empresa/human", CapabilityID: "organization.activate_skill", Risk: "high", ApprovalMode: "owner", ResourceType: "skill", ResourceID: "x", ActionDigest: digest, Status: RequestApproved, ExpiresAt: now.Add(time.Hour)}
	id := int64(10)
	got, err := service.Evaluate(context.Background(), EvaluationRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, ActorRoleID: "empresa/human", CapabilityID: "organization.activate_skill", ResourceType: "skill", ResourceID: "different", ActionDigest: digest, ApprovalRequestID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if got.Effect != EffectDeny || got.ReasonCode != ReasonApprovalScopeMismatch {
		t.Fatalf("evaluation=%+v", got)
	}
}

func TestConsumeRejectsOrganizationOverride(t *testing.T) {
	service, _, _ := testService(t)
	_, err := service.ConsumeApproval(context.Background(), ConsumeApprovalCommand{RequestID: 1, OrganizationID: "other", ActorRoleID: "empresa/human", ActionDigest: DigestAction([]byte("x"))})
	if !errors.Is(err, ErrOrganizationMismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestServiceValidationAndDefaultTTL(t *testing.T) {
	policy, reader := testAuthorizer(t)
	store := &fakeStore{organization: reader.organization, revision: reader.revision, role: reader.roles["empresa/human"]}
	if _, err := NewService(ServiceConfig{OrganizationID: "explorarte", DefaultTTL: 0, MaxTTL: time.Hour, ExpireBatchSize: 100, OutboxMaxAttempts: 10}, policy, store, ClockFunc(time.Now)); err == nil {
		t.Fatal("zero default TTL was accepted")
	}
	if _, err := NewService(ServiceConfig{OrganizationID: "explorarte", DefaultTTL: time.Hour, MaxTTL: time.Minute, ExpireBatchSize: 100, OutboxMaxAttempts: 10}, policy, store, ClockFunc(time.Now)); err == nil {
		t.Fatal("maximum TTL below default was accepted")
	}
}
