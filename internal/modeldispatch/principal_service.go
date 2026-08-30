package modeldispatch

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	capabilityPrincipalRegister          = "model.execution_principal.register"
	capabilityPrincipalDisable           = "model.execution_principal.disable"
	capabilityAssignmentCreate           = "model.dispatch_assignment.create"
	capabilityAssignmentRevoke           = "model.dispatch_assignment.revoke"
	capabilityProvisionAuthorizedAttempt = "model.dispatch_assignment.provision_authorized_attempt"
)

type PrincipalService struct {
	organizationID string
	authorizer     CapabilityAuthorizer
	catalog        OrganizationCatalog
	store          PrincipalStore
	clock          Clock
}

func NewPrincipalService(organizationID string, authorizer CapabilityAuthorizer, catalog OrganizationCatalog, store PrincipalStore, clock Clock) (*PrincipalService, error) {
	if authorizer == nil || catalog == nil || store == nil {
		return nil, fmt.Errorf("principal service dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &PrincipalService{organizationID: organizationID, authorizer: authorizer, catalog: catalog, store: store, clock: clock}, nil
}

func (s *PrincipalService) Register(ctx context.Context, actorRoleID string, command RegisterPrincipalCommand) (RegisterPrincipalResult, error) {
	actorRoleID = strings.TrimSpace(actorRoleID)
	if !roleIDPattern.MatchString(actorRoleID) {
		return RegisterPrincipalResult{}, fmt.Errorf("%w: actor role is required", ErrInvalidRequest)
	}
	if command.OrganizationID == "" {
		command.OrganizationID = s.organizationID
	}
	prepared, err := PrepareRegisterCommand(command)
	if err != nil {
		return RegisterPrincipalResult{}, err
	}
	revision, err := s.catalog.CurrentRevision(ctx, prepared.OrganizationID)
	if err != nil {
		return RegisterPrincipalResult{}, err
	}
	if err = s.authorizer.Authorize(ctx, prepared.OrganizationID, revision, actorRoleID, capabilityPrincipalRegister); err != nil {
		return RegisterPrincipalResult{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	role, err := s.catalog.GetRole(ctx, prepared.OrganizationID, prepared.DispatchActorRoleID)
	if err != nil {
		return RegisterPrincipalResult{}, err
	}
	if !eligibleDispatchActorRole(role) {
		return RegisterPrincipalResult{}, fmt.Errorf("%w: dispatch actor role must be enabled, executable and execution_service", ErrRoleNotEligible)
	}
	requestHash, err := PrincipalRequestHash(prepared.OrganizationID, prepared.PrincipalKey, prepared.DispatchActorRoleID, prepared.PrincipalKind, actorRoleID)
	if err != nil {
		return RegisterPrincipalResult{}, err
	}
	return s.store.RegisterPrincipal(ctx, PreparedRegisterPrincipal{Command: RegisterPrincipalCommand{
		OrganizationID: prepared.OrganizationID, PrincipalKey: prepared.PrincipalKey,
		DispatchActorRoleID: prepared.DispatchActorRoleID, PrincipalKind: prepared.PrincipalKind,
		IdempotencyKey: prepared.IdempotencyKey,
	}, RequestHash: requestHash, RegisteredByRoleID: actorRoleID})
}

func (s *PrincipalService) Get(ctx context.Context, id int64) (ExecutionPrincipal, error) {
	if id <= 0 {
		return ExecutionPrincipal{}, fmt.Errorf("%w: invalid principal ID", ErrInvalidRequest)
	}
	return s.store.GetPrincipal(ctx, id)
}

func (s *PrincipalService) List(ctx context.Context, organizationID string, limit int) ([]ExecutionPrincipal, error) {
	if organizationID == "" {
		organizationID = s.organizationID
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.store.ListPrincipals(ctx, organizationID, limit)
}

func (s *PrincipalService) Disable(ctx context.Context, actorRoleID string, principalID int64, reasonCode string) (ExecutionPrincipal, error) {
	actorRoleID = strings.TrimSpace(actorRoleID)
	reasonCode = strings.TrimSpace(reasonCode)
	if !roleIDPattern.MatchString(actorRoleID) || principalID <= 0 || !validReasonCode(reasonCode) {
		return ExecutionPrincipal{}, fmt.Errorf("%w: actor role, principal ID and reason code are required", ErrInvalidRequest)
	}
	revision, err := s.catalog.CurrentRevision(ctx, s.organizationID)
	if err != nil {
		return ExecutionPrincipal{}, err
	}
	if err = s.authorizer.Authorize(ctx, s.organizationID, revision, actorRoleID, capabilityPrincipalDisable); err != nil {
		return ExecutionPrincipal{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	return s.store.DisablePrincipal(ctx, principalID, actorRoleID, reasonCode)
}
