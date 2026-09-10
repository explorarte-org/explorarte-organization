package modeldispatch

import (
	"context"
	"time"
)

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

// CapabilityAuthorizer is structurally satisfied by internal/authorization.Authorizer.
type CapabilityAuthorizer interface {
	Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error
}

// ResourceCapabilityAuthorizer preserves the concrete task/attempt scope at
// the policy boundary. It is separate from the historical capability-only
// method so existing administrative operations retain their contract.
type ResourceCapabilityAuthorizer interface {
	AuthorizeResource(ctx context.Context, organizationID string, revisionID int64, roleID, capability, resourceType, resourceID, actionDigest string) error
}

type RoleRef struct {
	ID             string
	ModelPolicy    string
	Enabled        bool
	Executable     bool
	AuthorityClass string
}

// OrganizationCatalog resolves the current organization revision and role state.
type OrganizationCatalog interface {
	CurrentRevision(ctx context.Context, organizationID string) (int64, error)
	GetRole(ctx context.Context, organizationID, roleID string) (RoleRef, error)
}

type TaskAttemptRef struct {
	TaskID                 int64
	AttemptID              int64
	OrganizationID         string
	OrganizationRevisionID int64
	AssignedRoleID         string
	TaskStatus             string
	AttemptStatus          string
	LeaseHolderID          string
	LeaseExpiresAt         time.Time
}

// TaskAttemptReader resolves task/attempt/lease state used to scope an assignment.
type TaskAttemptReader interface {
	GetTaskAttempt(ctx context.Context, taskID, attemptID int64) (TaskAttemptRef, error)
}

// TaskLineageReader resolves only the immutable task identity needed to prove
// an attempt's durable provenance. The caller never supplies a requester role:
// the authorized-attempt boundary derives it by walking persisted causation.
type TaskLineageReader interface {
	GetTaskLineage(ctx context.Context, taskID int64) (TaskLineageRef, error)
}

// RoleRoutingAuthorityKind is which of the two disjoint sources actually
// authorizes a role's model calls at (organization, revision). A role
// resolves to exactly one -- never both, never neither -- see
// GetRoleRoutingAuthority.
type RoleRoutingAuthorityKind string

const (
	RoleRoutingStaticBinding RoleRoutingAuthorityKind = "static_binding"
	RoleRoutingPoolPolicy    RoleRoutingAuthorityKind = "pool_policy"
)

// RoleRoutingAuthorityRef is the provisioning-time authority gate for a
// role's model calls, unifying the two disjoint sources a role's
// organization_roles.model_policy can name: a static role_model_bindings
// row (routing_mode: static in docs/canonical/model-routing.yaml) or a
// materialized routing_policies row (routing_mode: pool -- see migration
// 000070's own comment: a pool-routed role has no role_model_bindings row
// at all). It carries no candidate, provider, or model -- selecting one of
// those stays exclusively RouteResolver's job, at Invocation-creation time,
// inside internal/modelruntime. ProfileID/ModelProfileVersionID are
// populated for Kind==RoleRoutingStaticBinding only; a pool authority is a
// policy-level fact, not a materialized profile, so both stay zero-valued
// for Kind==RoleRoutingPoolPolicy -- there is no "pool:<policy>" synthetic
// profile.
type RoleRoutingAuthorityRef struct {
	OrganizationID         string
	OrganizationRevisionID int64
	RoleID                 string
	PolicyID               string
	Kind                   RoleRoutingAuthorityKind
	AuthorityHash          string

	// STATIC only.
	ProfileID             string
	ModelProfileVersionID int64
}

// RoleRoutingAuthorityReader is the provisioning-time binding gate.
// Invocation creation deliberately revalidates authority later as a TOCTOU
// boundary. The caller never supplies a policyID: the reader derives
// organization_roles.model_policy for (organizationID, revisionID, roleID)
// itself and resolves exactly one authority for it, static XOR pool --
// both present or neither present fails closed.
type RoleRoutingAuthorityReader interface {
	GetRoleRoutingAuthority(ctx context.Context, organizationID string, revisionID int64, roleID string) (RoleRoutingAuthorityRef, error)
}

type AuthorizedAssignmentProvisioner interface {
	EnsureAuthorizedAssignmentForRunningAttempt(ctx context.Context, taskID, attemptID int64) (CreateAssignmentResult, error)
}

// AssignmentResolver is what modelruntime depends on to pin an invocation at
// creation time. It never selects a provider, model, or egress policy. It
// resolves by task/attempt/subject only; the caller is responsible for
// separately comparing the resolved assignment's organization revision
// against the currently active one to detect drift.
type AssignmentResolver interface {
	ResolveActive(ctx context.Context, organizationID string, taskID, attemptID int64, subjectRoleID string) (ResolvedAssignment, error)
	// GetByID returns the assignment regardless of its current status, so a
	// dispatcher can report a precise deny reason (revoked/expired/exhausted)
	// for an assignment an invocation was already pinned to at creation time.
	GetByID(ctx context.Context, organizationID string, assignmentID int64) (ResolvedAssignment, error)
}

// ExecutionPrincipalResolver is what modelruntime depends on to resolve the
// locally configured dispatcher identity before claiming an invocation.
type ExecutionPrincipalResolver interface {
	ResolveByKey(ctx context.Context, organizationID, principalKey string) (ExecutionPrincipal, error)
}

// RoleBoundPrincipalResolver resolves the single active execution principal
// authorized to send/act as a given organizational role -- the server-side
// trust boundary agent-messaging's per-hop sender authentication depends on.
// The database enforces at most one active row per (organization_id,
// dispatch_actor_role_id) (see migration 000048), so this never has to pick
// among candidates.
type RoleBoundPrincipalResolver interface {
	ResolveActiveForRole(ctx context.Context, organizationID, roleID string) (ExecutionPrincipal, error)
}

type PrincipalStore interface {
	RegisterPrincipal(context.Context, PreparedRegisterPrincipal) (RegisterPrincipalResult, error)
	GetPrincipal(context.Context, int64) (ExecutionPrincipal, error)
	ListPrincipals(context.Context, string, int) ([]ExecutionPrincipal, error)
	DisablePrincipal(context.Context, int64, string, string) (ExecutionPrincipal, error)
	ExecutionPrincipalResolver
	RoleBoundPrincipalResolver
}

type AssignmentStore interface {
	CreateAssignment(context.Context, PreparedCreateAssignment) (CreateAssignmentResult, error)
	GetAssignment(context.Context, int64) (DispatcherAssignment, error)
	ListAssignments(context.Context, string, int) ([]DispatcherAssignment, error)
	RevokeAssignment(context.Context, int64, string, string) (DispatcherAssignment, error)
	ExpireAssignments(context.Context, string, int, time.Time) (ExpireResult, error)
	AssignmentResolver
}

type Store interface {
	PrincipalStore
	AssignmentStore
}
