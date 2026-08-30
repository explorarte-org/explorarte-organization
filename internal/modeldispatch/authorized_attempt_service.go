package modeldispatch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	maxAuthorizedAttemptAncestryDepth = 64
	authorizedAttemptMaxInvocations   = 1
)

// AuthorizedAttemptProvisioner is the domain boundary for the one automatic
// assignment operation. It does not accept an actor or requester role: both
// identities are derived from persistence and re-authorized independently.
type AuthorizedAttemptProvisioner struct {
	assignments           *AssignmentService
	resourceAuthorizer    ResourceCapabilityAuthorizer
	lineage               TaskLineageReader
	bindings              RoleModelBindingReader
	executionPrincipalKey string
}

func NewAuthorizedAttemptProvisioner(assignments *AssignmentService, lineage TaskLineageReader, bindings RoleModelBindingReader, executionPrincipalKey string) (*AuthorizedAttemptProvisioner, error) {
	executionPrincipalKey = strings.TrimSpace(executionPrincipalKey)
	if assignments == nil || lineage == nil || bindings == nil {
		return nil, fmt.Errorf("authorized attempt provisioner dependencies are incomplete")
	}
	if len(executionPrincipalKey) < 1 || len(executionPrincipalKey) > 200 || !principalKeyPattern.MatchString(executionPrincipalKey) {
		return nil, fmt.Errorf("%w: invalid execution principal key", ErrInvalidRequest)
	}
	resourceAuthorizer, ok := assignments.authorizer.(ResourceCapabilityAuthorizer)
	if !ok {
		return nil, fmt.Errorf("authorized attempt provisioner requires resource-scoped authorization")
	}
	return &AuthorizedAttemptProvisioner{
		assignments: assignments, resourceAuthorizer: resourceAuthorizer, lineage: lineage, bindings: bindings,
		executionPrincipalKey: executionPrincipalKey,
	}, nil
}

// EnsureAuthorizedAssignmentForRunningAttempt creates the assignment once or
// returns the exact compatible assignment already created by this boundary.
// Any active assignment with a different principal, subject, requester, or
// effective role-model binding is an explicit conflict and is never replaced.
func (s *AuthorizedAttemptProvisioner) EnsureAuthorizedAssignmentForRunningAttempt(ctx context.Context, taskID, attemptID int64) (CreateAssignmentResult, error) {
	if taskID <= 0 || attemptID <= 0 {
		return CreateAssignmentResult{}, fmt.Errorf("%w: task and attempt are required", ErrInvalidRequest)
	}
	now := s.assignments.clock.Now().UTC()
	attempt, err := s.assignments.tasks.GetTaskAttempt(ctx, taskID, attemptID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if err = validateTaskAttemptForAssignment(attempt, s.assignments.organizationID, taskID, attemptID, attempt.AssignedRoleID, now); err != nil {
		return CreateAssignmentResult{}, err
	}
	revision, err := s.assignments.catalog.CurrentRevision(ctx, attempt.OrganizationID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if attempt.OrganizationRevisionID != revision {
		return CreateAssignmentResult{}, fmt.Errorf("%w: task organization revision drift", ErrRevisionDrift)
	}

	current, err := s.lineage.GetTaskLineage(ctx, taskID)
	if err != nil {
		return CreateAssignmentResult{}, fmt.Errorf("%w: load current task provenance: %v", ErrTaskAttemptRejected, err)
	}
	if current.TaskID != attempt.TaskID || current.OrganizationID != attempt.OrganizationID ||
		current.OrganizationRevisionID != attempt.OrganizationRevisionID || current.AssignedRoleID != attempt.AssignedRoleID {
		return CreateAssignmentResult{}, fmt.Errorf("%w: current task provenance does not match the running attempt", ErrTaskAttemptRejected)
	}
	root, err := s.resolveTrustedRoot(ctx, current)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	requesterRoleID := strings.TrimSpace(root.RequestedByRoleID)
	if !roleIDPattern.MatchString(requesterRoleID) {
		return CreateAssignmentResult{}, fmt.Errorf("%w: persisted root requester role is missing or invalid", ErrAuthorizationDenied)
	}

	principal, err := s.assignments.principals.ResolveByKey(ctx, attempt.OrganizationID, s.executionPrincipalKey)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if principal.OrganizationID != attempt.OrganizationID {
		return CreateAssignmentResult{}, fmt.Errorf("%w: execution principal organization mismatch", ErrPrincipalMismatch)
	}
	if principal.PrincipalKey != s.executionPrincipalKey {
		return CreateAssignmentResult{}, fmt.Errorf("%w: configured execution principal key mismatch", ErrPrincipalMismatch)
	}
	if principal.Status != PrincipalActive {
		return CreateAssignmentResult{}, fmt.Errorf("%w: configured execution principal is not active", ErrPrincipalDisabled)
	}
	dispatchRole, err := s.assignments.catalog.GetRole(ctx, attempt.OrganizationID, principal.DispatchActorRoleID)
	if err != nil {
		return CreateAssignmentResult{}, err
	}
	if !eligibleDispatchActorRole(dispatchRole) {
		return CreateAssignmentResult{}, fmt.Errorf("%w: dispatch actor role must be enabled, executable and execution_service", ErrRoleNotEligible)
	}
	binding, err := s.bindings.GetActiveRoleModelBinding(ctx, attempt.OrganizationID, revision, attempt.AssignedRoleID)
	if err != nil {
		return CreateAssignmentResult{}, fmt.Errorf("%w: active role-model binding: %v", ErrTaskAttemptRejected, err)
	}
	if !binding.Active || binding.OrganizationID != attempt.OrganizationID || binding.OrganizationRevisionID != revision ||
		binding.RoleID != attempt.AssignedRoleID || binding.ModelProfileVersionID <= 0 || !sha256Pattern.MatchString(binding.BindingHash) {
		return CreateAssignmentResult{}, fmt.Errorf("%w: active role-model binding scope mismatch", ErrTaskAttemptRejected)
	}
	resourceType := "model_dispatcher_assignment"
	resourceID := fmt.Sprintf("task:%d/attempt:%d", taskID, attemptID)
	actionDigest := authorizedAttemptActionDigest(root.TaskID, attempt, principal, binding, requesterRoleID)
	// Technical authority permits only this derivation operation. It never
	// receives model.dispatch_assignment.create and never impersonates the
	// durable requester passed to AssignmentService.Create below.
	if err = s.resourceAuthorizer.AuthorizeResource(ctx, attempt.OrganizationID, revision, principal.DispatchActorRoleID, capabilityProvisionAuthorizedAttempt, resourceType, resourceID, actionDigest); err != nil {
		return CreateAssignmentResult{}, fmt.Errorf("%w: technical provisioner: %v", ErrAuthorizationDenied, err)
	}
	// Resource authority comes from the persisted root and is evaluated at the
	// current revision for this exact task/attempt/binding action.
	if err = s.resourceAuthorizer.AuthorizeResource(ctx, attempt.OrganizationID, revision, requesterRoleID, capabilityAssignmentCreate, resourceType, resourceID, actionDigest); err != nil {
		return CreateAssignmentResult{}, fmt.Errorf("%w: durable requester: %v", ErrAuthorizationDenied, err)
	}

	idempotencyKey := authorizedAttemptIdempotencyKey(root.TaskID, attempt, principal, binding)
	if existing, resolveErr := s.assignments.store.ResolveActive(ctx, attempt.OrganizationID, taskID, attemptID, attempt.AssignedRoleID); resolveErr == nil {
		if err = validateAuthorizedAttemptReplay(existing.Assignment, attempt, revision, principal, requesterRoleID, idempotencyKey, now); err != nil {
			return CreateAssignmentResult{}, err
		}
		return CreateAssignmentResult{Assignment: existing.Assignment, Reused: true}, nil
	} else if !errors.Is(resolveErr, ErrNotFound) && !errors.Is(resolveErr, ErrAssignmentNotFound) {
		return CreateAssignmentResult{}, resolveErr
	}

	validUntil := now.Add(s.assignments.defaultTTL)
	if attempt.LeaseExpiresAt.Before(validUntil) {
		validUntil = attempt.LeaseExpiresAt.UTC()
	}
	result, err := s.assignments.Create(ctx, requesterRoleID, CreateAssignmentCommand{
		OrganizationID: attempt.OrganizationID, TaskID: taskID, AttemptID: attemptID,
		SubjectRoleID: attempt.AssignedRoleID, ExecutionPrincipalKey: principal.PrincipalKey,
		MaxInvocations: authorizedAttemptMaxInvocations, ValidUntil: &validUntil,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if errors.Is(err, ErrAssignmentConflict) || errors.Is(err, ErrConflict) {
			return CreateAssignmentResult{}, fmt.Errorf("%w: divergent assignment already exists for task %d attempt %d", ErrConflict, taskID, attemptID)
		}
		return CreateAssignmentResult{}, err
	}
	if err = validateAuthorizedAttemptReplay(result.Assignment, attempt, revision, principal, requesterRoleID, idempotencyKey, now); err != nil {
		return CreateAssignmentResult{}, err
	}
	return result, nil
}

func (s *AuthorizedAttemptProvisioner) resolveTrustedRoot(ctx context.Context, current TaskLineageRef) (TaskLineageRef, error) {
	visited := make(map[int64]struct{}, maxAuthorizedAttemptAncestryDepth)
	for depth := 0; depth < maxAuthorizedAttemptAncestryDepth; depth++ {
		if current.TaskID <= 0 {
			return TaskLineageRef{}, fmt.Errorf("%w: provenance contains an invalid task ID", ErrTaskAttemptRejected)
		}
		if _, repeated := visited[current.TaskID]; repeated {
			return TaskLineageRef{}, fmt.Errorf("%w: provenance cycle at task %d", ErrTaskAttemptRejected, current.TaskID)
		}
		visited[current.TaskID] = struct{}{}
		cause := strings.TrimSpace(current.CausationID)
		if validOwnerRootCausation(cause) {
			return current, nil
		}
		parentID, parseErr := parseTaskCausation(cause)
		if parseErr != nil {
			return TaskLineageRef{}, fmt.Errorf("%w: task %d has unsupported causation %q", ErrTaskAttemptRejected, current.TaskID, cause)
		}
		parent, loadErr := s.lineage.GetTaskLineage(ctx, parentID)
		if loadErr != nil {
			return TaskLineageRef{}, fmt.Errorf("%w: task %d parent %d is unavailable: %v", ErrTaskAttemptRejected, current.TaskID, parentID, loadErr)
		}
		if parent.TaskID != parentID || parent.TaskID == current.TaskID || parent.OrganizationID != current.OrganizationID ||
			strings.TrimSpace(parent.CorrelationID) == "" ||
			parent.CorrelationID != current.CorrelationID {
			return TaskLineageRef{}, fmt.Errorf("%w: task %d parent provenance is incompatible", ErrTaskAttemptRejected, current.TaskID)
		}
		current = parent
	}
	return TaskLineageRef{}, fmt.Errorf("%w: provenance exceeds depth limit %d", ErrTaskAttemptRejected, maxAuthorizedAttemptAncestryDepth)
}

func parseTaskCausation(value string) (int64, error) {
	if !strings.HasPrefix(value, "task:") {
		return 0, ErrInvalidRequest
	}
	raw := strings.TrimPrefix(value, "task:")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || raw != strconv.FormatInt(id, 10) {
		return 0, ErrInvalidRequest
	}
	return id, nil
}

func validOwnerRootCausation(value string) bool {
	if !strings.HasPrefix(value, "owner:") {
		return false
	}
	suffix := strings.TrimPrefix(value, "owner:")
	return len(suffix) >= 1 && len(value) <= 200 && principalKeyPattern.MatchString(suffix)
}

func authorizedAttemptIdempotencyKey(rootTaskID int64, attempt TaskAttemptRef, principal ExecutionPrincipal, binding RoleModelBindingRef) string {
	body := strings.Join([]string{
		attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
		binding.ProfileID, strconv.FormatInt(binding.ModelProfileVersionID, 10), binding.BindingHash,
	}, "\x00")
	return fmt.Sprintf("authorized-attempt/%d/%d/%s", attempt.TaskID, attempt.AttemptID, sha256Hex([]byte(body))[:32])
}

func authorizedAttemptActionDigest(rootTaskID int64, attempt TaskAttemptRef, principal ExecutionPrincipal, binding RoleModelBindingRef, requesterRoleID string) string {
	body := strings.Join([]string{
		"provision_authorized_attempt", attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), requesterRoleID, strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
		binding.ProfileID, strconv.FormatInt(binding.ModelProfileVersionID, 10), binding.BindingHash,
	}, "\x00")
	return sha256Hex([]byte(body))
}

func validateAuthorizedAttemptReplay(assignment DispatcherAssignment, attempt TaskAttemptRef, revision int64, principal ExecutionPrincipal, requesterRoleID, idempotencyKey string, now time.Time) error {
	if assignment.OrganizationID != attempt.OrganizationID || assignment.OrganizationRevisionID != revision ||
		assignment.TaskID != attempt.TaskID || assignment.AttemptID != attempt.AttemptID || assignment.SubjectRoleID != attempt.AssignedRoleID ||
		assignment.ExecutionPrincipalID != principal.ID || assignment.DispatchActorRoleID != principal.DispatchActorRoleID ||
		assignment.Status != AssignmentActive || assignment.IdempotencyKey != idempotencyKey ||
		assignment.CreatedByRoleID != requesterRoleID || assignment.MaxInvocations != authorizedAttemptMaxInvocations ||
		assignment.UsedInvocations >= assignment.MaxInvocations || !assignment.ValidUntil.After(now) {
		return fmt.Errorf("%w: existing assignment is incompatible with the authorized attempt", ErrConflict)
	}
	return nil
}
