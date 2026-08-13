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

type RoleRef struct {
	ID             string
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
