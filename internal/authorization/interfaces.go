package authorization

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type CapabilityAuthorizer interface {
	Authorize(context.Context, string, int64, string, string) error
}

type Evaluator interface {
	Evaluate(context.Context, EvaluationRequest) (Evaluation, error)
}

type ApprovalService interface {
	RequestApproval(context.Context, RequestApprovalCommand) (RequestApprovalResult, error)
	DecideRequest(context.Context, DecideRequestCommand) (ApprovalRequest, error)
	ConsumeApproval(context.Context, ConsumeApprovalCommand) (ConsumeApprovalResult, error)
	CancelRequest(context.Context, CancelRequestCommand) (ApprovalRequest, error)
	ExpireRequests(context.Context, int) (ExpireResult, error)
	GetRequest(context.Context, int64) (ApprovalRequest, error)
	ListRequests(context.Context, ListRequestsFilter) ([]ApprovalRequest, error)
}

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type PolicyReader interface {
	GetOrganization(context.Context, string) (registry.Organization, error)
	GetCurrentRevision(context.Context, string) (*registry.Revision, error)
	GetAuthorizationRole(context.Context, string, string) (registry.Role, error)
}

type ApprovalValidationContext struct {
	Request      ApprovalRequest
	Organization registry.Organization
	Revision     registry.Revision
	Role         registry.Role
}

type ApprovalValidator func(ApprovalValidationContext, ConsumeApprovalCommand) ReasonCode

type DecisionValidator func(ApprovalValidationContext) ReasonCode

type Store interface {
	PolicyReader
	CreateRequest(context.Context, ApprovalRequest, int) (RequestApprovalResult, error)
	DecideRequest(context.Context, DecideRequestCommand, time.Time, DecisionValidator, int) (ApprovalRequest, error)
	ConsumeApproval(context.Context, ConsumeApprovalCommand, time.Time, ApprovalValidator, int) (ConsumeApprovalResult, error)
	CancelRequest(context.Context, CancelRequestCommand, time.Time, int) (ApprovalRequest, error)
	ExpireRequests(context.Context, string, time.Time, int, int) (ExpireResult, error)
	GetRequest(context.Context, int64) (ApprovalRequest, error)
	ListRequests(context.Context, ListRequestsFilter) ([]ApprovalRequest, error)
}
