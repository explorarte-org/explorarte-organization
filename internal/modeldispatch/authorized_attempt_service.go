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
	// authorizedAttemptMaxInvocations is the DEFAULT quota -- one model
	// invocation per authorized attempt -- unchanged from before this
	// policy became configurable. Every existing call site that does not
	// pass WithMaxInvocations keeps this exact value, so Executive typed
	// tasks (one task/attempt = one model call, no in-Harness tool loop)
	// are byte-for-byte unaffected.
	authorizedAttemptMaxInvocations = 1
	// authorizedAttemptMaxInvocationsCeiling bounds how large a caller may
	// configure the per-attempt invocation quota to be. It exists so this
	// policy can never be configured as effectively unlimited (the DB
	// column itself permits up to 1,000,000 -- see
	// model_dispatcher_assignments.max_invocations -- but that ceiling is
	// sized for long-lived, non-conversational assignments in general and
	// is not an appropriate bound for a single bounded execution grant).
	// 64 is deliberately generous headroom above every execution profile
	// that exists today (ceochat's executive/chat/v1 needs exactly
	// ceochat.MaxTurns = 8; Executive's typed-task profile needs exactly
	// 1) while still being small enough that a misconfigured caller could
	// never turn this into an effectively unbounded grant.
	authorizedAttemptMaxInvocationsCeiling = 64
)

// AuthorizedAttemptProvisioner is the domain boundary for the one automatic
// assignment operation. It does not accept an actor or requester role: both
// identities are derived from persistence and re-authorized independently.
//
// maxInvocations is fixed at construction time -- it is HOST-owned policy,
// never a per-call parameter. EnsureAuthorizedAssignmentForRunningAttempt
// deliberately takes no quota argument: if it did, every call site would be
// a potential source of authority (an owner message, model output, tool
// arguments, task instructions -- none of those may ever choose how many
// model invocations an attempt is allowed).
type AuthorizedAttemptProvisioner struct {
	assignments           *AssignmentService
	resourceAuthorizer    ResourceCapabilityAuthorizer
	lineage               TaskLineageReader
	authority             RoleRoutingAuthorityReader
	executionPrincipalKey string
	maxInvocations        int
}

// AuthorizedAttemptProvisionerOption configures optional
// AuthorizedAttemptProvisioner behavior that most callers (in particular
// every existing Executive call site) don't need to set.
type AuthorizedAttemptProvisionerOption func(*AuthorizedAttemptProvisioner)

// WithMaxInvocations overrides the default single-invocation quota. It
// exists for execution surfaces whose Harness policy can legitimately make
// more than one model invocation within a single task attempt (e.g. a
// conversational turn that may call read-only tools across several model
// round-trips before answering). The value is validated at construction,
// never at dispatch time, so a misconfigured policy fails loudly at
// bootstrap rather than as a confusing runtime denial deep into a run.
func WithMaxInvocations(n int) AuthorizedAttemptProvisionerOption {
	return func(p *AuthorizedAttemptProvisioner) { p.maxInvocations = n }
}

func NewAuthorizedAttemptProvisioner(assignments *AssignmentService, lineage TaskLineageReader, authority RoleRoutingAuthorityReader, executionPrincipalKey string, opts ...AuthorizedAttemptProvisionerOption) (*AuthorizedAttemptProvisioner, error) {
	executionPrincipalKey = strings.TrimSpace(executionPrincipalKey)
	if assignments == nil || lineage == nil || authority == nil {
		return nil, fmt.Errorf("authorized attempt provisioner dependencies are incomplete")
	}
	if len(executionPrincipalKey) < 1 || len(executionPrincipalKey) > 200 || !principalKeyPattern.MatchString(executionPrincipalKey) {
		return nil, fmt.Errorf("%w: invalid execution principal key", ErrInvalidRequest)
	}
	resourceAuthorizer, ok := assignments.authorizer.(ResourceCapabilityAuthorizer)
	if !ok {
		return nil, fmt.Errorf("authorized attempt provisioner requires resource-scoped authorization")
	}
	provisioner := &AuthorizedAttemptProvisioner{
		assignments: assignments, resourceAuthorizer: resourceAuthorizer, lineage: lineage, authority: authority,
		executionPrincipalKey: executionPrincipalKey, maxInvocations: authorizedAttemptMaxInvocations,
	}
	for _, opt := range opts {
		opt(provisioner)
	}
	if provisioner.maxInvocations < 1 || provisioner.maxInvocations > authorizedAttemptMaxInvocationsCeiling {
		return nil, fmt.Errorf("%w: authorized attempt max invocations must be between 1 and %d", ErrInvalidRequest, authorizedAttemptMaxInvocationsCeiling)
	}
	return provisioner, nil
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
	// GetRoleRoutingAuthority derives the role's model_policy itself and
	// resolves exactly one authority for it -- static role_model_bindings
	// XOR materialized pool routing_policies, both present or neither
	// present fails closed (see the Postgres implementation's doc comment).
	// No candidate, provider, or model crosses into modeldispatch here:
	// RouteResolver alone picks one, per-invocation, at Invocation-creation
	// time inside internal/modelruntime.
	authority, err := s.authority.GetRoleRoutingAuthority(ctx, attempt.OrganizationID, revision, attempt.AssignedRoleID)
	if err != nil {
		return CreateAssignmentResult{}, fmt.Errorf("%w: role routing authority: %v", ErrTaskAttemptRejected, err)
	}
	resourceType := "model_dispatcher_assignment"
	resourceID := fmt.Sprintf("task:%d/attempt:%d", taskID, attemptID)
	actionDigest := authorizedAttemptActionDigest(root.TaskID, attempt, principal, authority, requesterRoleID, s.maxInvocations)
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

	idempotencyKey := authorizedAttemptIdempotencyKey(root.TaskID, attempt, principal, authority, s.maxInvocations)
	if existing, resolveErr := s.assignments.store.ResolveActive(ctx, attempt.OrganizationID, taskID, attemptID, attempt.AssignedRoleID); resolveErr == nil {
		if err = validateAuthorizedAttemptReplay(existing.Assignment, attempt, revision, principal, requesterRoleID, idempotencyKey, s.maxInvocations, now); err != nil {
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
		MaxInvocations: s.maxInvocations, ValidUntil: &validUntil,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if errors.Is(err, ErrAssignmentConflict) || errors.Is(err, ErrConflict) {
			return CreateAssignmentResult{}, fmt.Errorf("%w: divergent assignment already exists for task %d attempt %d", ErrConflict, taskID, attemptID)
		}
		return CreateAssignmentResult{}, err
	}
	if err = validateAuthorizedAttemptReplay(result.Assignment, attempt, revision, principal, requesterRoleID, idempotencyKey, s.maxInvocations, now); err != nil {
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

// authorityDigestFields is the authority-derived tail shared by
// authorizedAttemptIdempotencyKey and authorizedAttemptActionDigest.
//
// STATIC reuses exactly the three fields the pre-authority-unification
// binding carried (ProfileID, ModelProfileVersionID, its hash) in the same
// order -- byte-for-byte identical digests for every existing static
// assignment, so replay/idempotency for already-provisioned static attempts
// is untouched by this change.
//
// POOL is explicitly domain-separated from STATIC with a literal
// "pool_policy" tag ahead of PolicyID and AuthorityHash
// (routing_policies.canonical_hash) -- never a candidate, provider, or
// model, which RouteResolver alone selects later, per-invocation. Without
// this tag a pool authority and a static authority that happened to reuse
// the same string values in the same field positions could collide; the
// tag makes that structurally impossible.
func authorityDigestFields(authority RoleRoutingAuthorityRef) []string {
	if authority.Kind == RoleRoutingPoolPolicy {
		return []string{"pool_policy", authority.PolicyID, authority.AuthorityHash}
	}
	return []string{authority.ProfileID, strconv.FormatInt(authority.ModelProfileVersionID, 10), authority.AuthorityHash}
}

// legacyAuthorizedAttemptMaxInvocations names the one quota value that ever
// existed before CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1 made
// this policy configurable. It is a separate name from
// authorizedAttemptMaxInvocations (the current default) purely so the
// digest functions below read as "the historical body shape applies
// exactly when this legacy value is requested" rather than "applies
// exactly when nobody overrode the current default" -- those happen to be
// the same number today, but the compatibility contract is about the
// HISTORICAL shape, not about default-ness.
const legacyAuthorizedAttemptMaxInvocations = 1

// authorizedAttemptIdempotencyKey and authorizedAttemptActionDigest below
// carry a durable-compatibility contract, not just an internal digest
// choice: real Postgres assignment rows already exist (or existed during a
// rollout window) whose idempotency_key/request_hash were computed by the
// pre-quota-policy formula, for MaxInvocations=1 only (the only value that
// could ever be requested before this change). A rolling deploy, or simply
// resuming a long-lived attempt across a restart, must still recognize
// those rows as the exact same grant -- never as a conflict, never by
// silently minting a second assignment.
//
// So for maxInvocations == legacyAuthorizedAttemptMaxInvocations (1), the
// body is BYTE-IDENTICAL to the pre-change formula: no max_invocations
// field, "authorized-attempt/..." key prefix, "provision_authorized_attempt"
// digest prefix -- unchanged, on purpose, forever, for this one value.
//
// For any other maxInvocations, the body is explicitly domain-separated
// (a literal "multi_invocation" tag plus the decimal quota, and a distinct
// "authorized-attempt-multi/..." key prefix) so a multi-invocation grant
// can never collide with, or be silently reinterpreted as, a legacy
// single-invocation one -- see
// TestAuthorizedAttemptDigestsAreDomainSeparatedByMaxInvocations and
// TestAuthorizedAttemptLegacyMax1DigestsAreByteIdenticalToThePreQuotaFormula.
func authorizedAttemptIdempotencyKey(rootTaskID int64, attempt TaskAttemptRef, principal ExecutionPrincipal, authority RoleRoutingAuthorityRef, maxInvocations int) string {
	fields := []string{
		attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
	}
	keyPrefix := "authorized-attempt"
	if maxInvocations != legacyAuthorizedAttemptMaxInvocations {
		fields = append(fields, "multi_invocation", strconv.Itoa(maxInvocations))
		keyPrefix = "authorized-attempt-multi"
	}
	fields = append(fields, authorityDigestFields(authority)...)
	body := strings.Join(fields, "\x00")
	return fmt.Sprintf("%s/%d/%d/%s", keyPrefix, attempt.TaskID, attempt.AttemptID, sha256Hex([]byte(body))[:32])
}

func authorizedAttemptActionDigest(rootTaskID int64, attempt TaskAttemptRef, principal ExecutionPrincipal, authority RoleRoutingAuthorityRef, requesterRoleID string, maxInvocations int) string {
	fields := []string{
		"provision_authorized_attempt", attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), requesterRoleID, strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
	}
	if maxInvocations != legacyAuthorizedAttemptMaxInvocations {
		fields = append(fields, "multi_invocation", strconv.Itoa(maxInvocations))
	}
	fields = append(fields, authorityDigestFields(authority)...)
	body := strings.Join(fields, "\x00")
	return sha256Hex([]byte(body))
}

func validateAuthorizedAttemptReplay(assignment DispatcherAssignment, attempt TaskAttemptRef, revision int64, principal ExecutionPrincipal, requesterRoleID, idempotencyKey string, maxInvocations int, now time.Time) error {
	if assignment.OrganizationID != attempt.OrganizationID || assignment.OrganizationRevisionID != revision ||
		assignment.TaskID != attempt.TaskID || assignment.AttemptID != attempt.AttemptID || assignment.SubjectRoleID != attempt.AssignedRoleID ||
		assignment.ExecutionPrincipalID != principal.ID || assignment.DispatchActorRoleID != principal.DispatchActorRoleID ||
		assignment.Status != AssignmentActive || assignment.IdempotencyKey != idempotencyKey ||
		assignment.CreatedByRoleID != requesterRoleID || assignment.MaxInvocations != maxInvocations ||
		assignment.UsedInvocations >= assignment.MaxInvocations || !assignment.ValidUntil.After(now) {
		return fmt.Errorf("%w: existing assignment is incompatible with the authorized attempt", ErrConflict)
	}
	return nil
}
