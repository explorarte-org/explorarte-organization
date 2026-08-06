package modeldispatch

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type AssignmentService struct {
	organizationID string
	authorizer     CapabilityAuthorizer
	catalog        OrganizationCatalog
	tasks          TaskAttemptReader
	principals     ExecutionPrincipalResolver
	store          AssignmentStore
	clock          Clock
	defaultTTL     time.Duration
	maxTTL         time.Duration
}

func NewAssignmentService(organizationID string, authorizer CapabilityAuthorizer, catalog OrganizationCatalog, tasks TaskAttemptReader, principals ExecutionPrincipalResolver, store AssignmentStore, clock Clock, defaultTTL, maxTTL time.Duration) (*AssignmentService, error) {
	if authorizer == nil || catalog == nil || tasks == nil || principals == nil || store == nil {
		return nil, fmt.Errorf("assignment service dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	if defaultTTL <= 0 || maxTTL <= 0 || defaultTTL > maxTTL {
		return nil, fmt.Errorf("%w: invalid assignment TTL configuration", ErrInvalidRequest)
	}
	return &AssignmentService{organizationID: organizationID, authorizer: authorizer, catalog: catalog, tasks: tasks, principals: principals, store: store, clock: clock, defaultTTL: defaultTTL, maxTTL: maxTTL}, nil
}

func (s *AssignmentService) Create(ctx context.Context, actorRoleID string, command CreateAssignmentCommand) (CreateAssignmentResult, error) {
	actorRoleID = strings.TrimSpace(actorRoleID)
	if !roleIDPattern.MatchString(actorRoleID) {
		return CreateAssignmentResult{}, fmt.Errorf("%w: actor role is required", ErrInvalidRequest)
	}
	if command.OrganizationID == "" {
		command.OrganizationID = s.organizationID
	}
	now := s.clock.Now()
	prepared, validFrom, validUntil, err := PrepareCreateAssignmentCommand(command, now, s.defaultTTL, s.maxTTL)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	revision, err := s.catalog.CurrentRevision(ctx, prepared.OrganizationID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if err = s.authorizer.Authorize(ctx, prepared.OrganizationID, revision, actorRoleID, capabilityAssignmentCreate); err != nil {
		return CreateAssignmentResult{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	taskAttempt, err := s.tasks.GetTaskAttempt(ctx, prepared.TaskID, prepared.AttemptID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if err = validateTaskAttemptForAssignment(taskAttempt, prepared.OrganizationID, prepared.TaskID, prepared.AttemptID, prepared.SubjectRoleID, now); err != nil {
		return CreateAssignmentResult{}, err
	}
	if taskAttempt.OrganizationRevisionID != revision {
		return CreateAssignmentResult{}, fmt.Errorf("%w: task organization revision drift", ErrRevisionDrift)
	}
	if validUntil.After(taskAttempt.LeaseExpiresAt) {
		return CreateAssignmentResult{}, fmt.Errorf("%w: assignment vigency exceeds the active task lease", ErrInvalidRequest)
	}
	principal, err := s.principals.ResolveByKey(ctx, prepared.OrganizationID, prepared.ExecutionPrincipalKey)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if principal.Status != PrincipalActive {
		return CreateAssignmentResult{}, ErrPrincipalDisabled
	}
	role, err := s.catalog.GetRole(ctx, prepared.OrganizationID, principal.DispatchActorRoleID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if !eligibleDispatchActorRole(role) {
		return CreateAssignmentResult{}, fmt.Errorf("%w: dispatch actor role must be enabled, executable and execution_service", ErrRoleNotEligible)
	}
	assignmentHash, err := AssignmentScopeHash(prepared.OrganizationID, revision, prepared.TaskID, prepared.AttemptID, prepared.SubjectRoleID, principal.DispatchActorRoleID, principal.ID, prepared.MaxInvocations, validFrom, validUntil)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	requestHash, err := AssignmentRequestHash(prepared.OrganizationID, revision, prepared.TaskID, prepared.AttemptID, prepared.SubjectRoleID, principal.ID, principal.PrincipalKey, principal.DispatchActorRoleID, validFrom, validUntil, prepared.MaxInvocations, actorRoleID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	return s.store.CreateAssignment(ctx, PreparedCreateAssignment{
		Command: CreateAssignmentCommand{
			OrganizationID: prepared.OrganizationID, TaskID: prepared.TaskID, AttemptID: prepared.AttemptID,
			SubjectRoleID: prepared.SubjectRoleID, ExecutionPrincipalKey: prepared.ExecutionPrincipalKey,
			MaxInvocations: prepared.MaxInvocations, IdempotencyKey: prepared.IdempotencyKey,
		},
		Principal: principal, OrganizationRevisionID: revision,
		ValidFrom: validFrom, ValidUntil: validUntil,
		AssignmentHash: assignmentHash, RequestHash: requestHash,
		CreatedByRoleID: actorRoleID,
	})
}

func (s *AssignmentService) Get(ctx context.Context, id int64) (DispatcherAssignment, error) {
	if id <= 0 {
		return DispatcherAssignment{}, fmt.Errorf("%w: invalid assignment ID", ErrInvalidRequest)
	}
	return s.store.GetAssignment(ctx, id)
}

func (s *AssignmentService) List(ctx context.Context, organizationID string, limit int) ([]DispatcherAssignment, error) {
	if organizationID == "" {
		organizationID = s.organizationID
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.store.ListAssignments(ctx, organizationID, limit)
}

func (s *AssignmentService) Revoke(ctx context.Context, actorRoleID string, assignmentID int64, reasonCode string) (DispatcherAssignment, error) {
	actorRoleID = strings.TrimSpace(actorRoleID)
	reasonCode = strings.TrimSpace(reasonCode)
	if !roleIDPattern.MatchString(actorRoleID) || assignmentID <= 0 || !validReasonCode(reasonCode) {
		return DispatcherAssignment{}, fmt.Errorf("%w: actor role, assignment ID and reason code are required", ErrInvalidRequest)
	}
	revision, err := s.catalog.CurrentRevision(ctx, s.organizationID)
	if err != nil {
		return DispatcherAssignment{}, err
	}
	if err = s.authorizer.Authorize(ctx, s.organizationID, revision, actorRoleID, capabilityAssignmentRevoke); err != nil {
		return DispatcherAssignment{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	return s.store.RevokeAssignment(ctx, assignmentID, actorRoleID, reasonCode)
}

func (s *AssignmentService) Expire(ctx context.Context, organizationID string, batch int) (ExpireResult, error) {
	if organizationID == "" {
		organizationID = s.organizationID
	}
	if batch <= 0 {
		batch = 100
	}
	return s.store.ExpireAssignments(ctx, organizationID, batch, s.clock.Now())
}
