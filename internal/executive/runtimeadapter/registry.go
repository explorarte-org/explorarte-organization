package runtimeadapter

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type Registry struct {
	Reader         registry.Reader
	OrganizationID string
}

func (a Registry) CurrentRevision(ctx context.Context) (executive.RevisionRef, error) {
	revision, err := a.Reader.GetCurrentRevision(ctx, a.OrganizationID)
	if err != nil {
		return executive.RevisionRef{}, err
	}
	if revision == nil {
		return executive.RevisionRef{}, registry.ErrNotFound
	}
	return executive.RevisionRef{ID: revision.ID, CanonicalHash: revision.CanonicalHash}, nil
}

func (a Registry) GetUnit(ctx context.Context, id string) (executive.UnitRef, error) {
	unit, err := a.Reader.GetUnit(ctx, a.OrganizationID, id)
	if err != nil {
		return executive.UnitRef{}, err
	}
	leader := ""
	if unit.LeaderRoleID != nil {
		leader = *unit.LeaderRoleID
	}
	return executive.UnitRef{
		ID:           unit.ID,
		Operational:  unit.Operational,
		Leaderless:   unit.Leaderless,
		LeaderRoleID: leader,
		Retired:      unit.RetiredAt != nil,
	}, nil
}

func (a Registry) GetRole(ctx context.Context, id string) (executive.RoleRef, error) {
	role, err := a.Reader.GetRole(ctx, a.OrganizationID, id)
	if err != nil {
		return executive.RoleRef{}, err
	}
	return mapRole(role), nil
}

func (a Registry) GetLeader(ctx context.Context, unit string) (executive.RoleRef, error) {
	role, err := a.Reader.GetLeader(ctx, a.OrganizationID, unit)
	if err != nil {
		return executive.RoleRef{}, err
	}
	return mapRole(role), nil
}

func mapRole(role registry.Role) executive.RoleRef {
	return executive.RoleRef{
		ID:              role.ID,
		UnitID:          role.UnitID,
		Enabled:         role.Enabled,
		Executable:      role.Executable,
		Retired:         role.RetiredAt != nil,
		CanonicalLeader: role.CanonicalLeader,
		AuthorityClass:  role.AuthorityClass,
	}
}

type Assignment struct {
	Resolver       modeldispatch.AssignmentResolver
	Provisioner    modeldispatch.AuthorizedAssignmentProvisioner
	OrganizationID string
}

func (a Assignment) EnsureAuthorizedAssignmentForRunningAttempt(ctx context.Context, taskID, attemptID int64) (executive.AssignmentRef, error) {
	if a.Provisioner == nil {
		return executive.AssignmentRef{}, fmt.Errorf("authorized assignment provisioner is unavailable")
	}
	result, err := a.Provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, taskID, attemptID)
	if err != nil {
		return executive.AssignmentRef{}, err
	}
	return assignmentRef(result.Assignment), nil
}

func (a Assignment) ResolveAssignment(ctx context.Context, taskID, attemptID int64, role string) (executive.AssignmentRef, error) {
	resolved, err := a.Resolver.ResolveActive(ctx, a.OrganizationID, taskID, attemptID, role)
	if err != nil {
		return executive.AssignmentRef{}, err
	}
	return assignmentRef(resolved.Assignment), nil
}

func assignmentRef(assignment modeldispatch.DispatcherAssignment) executive.AssignmentRef {
	return executive.AssignmentRef{
		ID: assignment.ID, OrganizationRevisionID: assignment.OrganizationRevisionID,
		TaskID: assignment.TaskID, AttemptID: assignment.AttemptID, SubjectRoleID: assignment.SubjectRoleID,
		ExecutionPrincipalID: assignment.ExecutionPrincipalID, DispatchActorRoleID: assignment.DispatchActorRoleID,
		ValidUntil: assignment.ValidUntil,
	}
}

type Completion struct{ Service *completion.Service }

func (a Completion) Verify(ctx context.Context, taskID, attemptID int64) (executive.CompletionResult, error) {
	result, err := a.Service.Verify(ctx, completion.VerificationRequest{TaskID: taskID, AttemptID: attemptID})
	if err != nil {
		return executive.CompletionResult{}, err
	}
	detail := ""
	for _, obligation := range result.Obligations {
		if obligation.Label == completion.LabelUnknown || obligation.Label == completion.LabelContradicted {
			detail = obligation.Detail
			if detail != "" {
				break
			}
		}
	}
	return executive.CompletionResult{Verdict: executive.CompletionVerdict(result.Verdict), Detail: detail}, nil
}

type Authorization struct {
	Service        *authorization.Service
	OrganizationID string
}

func (a Authorization) Evaluate(ctx context.Context, request executive.AuthorizationRequest) (executive.AuthorizationDecision, error) {
	result, err := a.Service.Evaluate(ctx, authorization.EvaluationRequest{
		OrganizationID:         a.OrganizationID,
		OrganizationRevisionID: request.OrganizationRevisionID,
		ActorRoleID:            request.ActorRoleID,
		CapabilityID:           request.CapabilityID,
		ResourceType:           request.ResourceType,
		ResourceID:             request.ResourceID,
		ActionDigest:           request.ActionDigest,
		ApprovalRequestID:      request.ApprovalRequestID,
	})
	if err != nil {
		return executive.AuthorizationDecision{}, err
	}
	return executive.AuthorizationDecision{
		Allowed:    result.Effect == authorization.EffectAllow,
		Effect:     string(result.Effect),
		ReasonCode: string(result.ReasonCode),
	}, nil
}

func ValidateStaticDependencies(reader registry.Reader, resolver modeldispatch.AssignmentResolver, completionService *completion.Service, authorizationService *authorization.Service) error {
	if reader == nil || resolver == nil || completionService == nil || authorizationService == nil {
		return fmt.Errorf("executive runtime adapter dependencies are incomplete")
	}
	return nil
}

var _ executive.RegistryResolver = Registry{}
var _ executive.DispatchProvisioner = Assignment{}
var _ executive.CompletionGate = Completion{}
var _ executive.AuthorizationGate = Authorization{}
