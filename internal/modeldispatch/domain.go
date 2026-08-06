package modeldispatch

import "time"

type PrincipalKind string

const (
	PrincipalLocalProcess PrincipalKind = "local_process"
	PrincipalCellProcess  PrincipalKind = "cell_process"
)

type PrincipalStatus string

const (
	PrincipalActive   PrincipalStatus = "active"
	PrincipalDisabled PrincipalStatus = "disabled"
)

type AssignmentStatus string

const (
	AssignmentActive    AssignmentStatus = "active"
	AssignmentExhausted AssignmentStatus = "exhausted"
	AssignmentExpired   AssignmentStatus = "expired"
	AssignmentRevoked   AssignmentStatus = "revoked"
)

func (s AssignmentStatus) Terminal() bool {
	return s == AssignmentExhausted || s == AssignmentExpired || s == AssignmentRevoked
}

// ExecutionPrincipal identifies a concrete instance of a dispatching process. It
// is not an agent, carries no context or memory, and never stores credentials.
type ExecutionPrincipal struct {
	ID                  int64           `json:"id"`
	OrganizationID      string          `json:"organization_id"`
	PrincipalKey        string          `json:"principal_key"`
	DispatchActorRoleID string          `json:"dispatch_actor_role_id"`
	PrincipalKind       PrincipalKind   `json:"principal_kind"`
	Status              PrincipalStatus `json:"status"`
	IdempotencyKey      string          `json:"idempotency_key"`
	RequestHash         string          `json:"request_hash"`
	RegisteredByRoleID  string          `json:"registered_by_role_id"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	DisabledAt          *time.Time      `json:"disabled_at,omitempty"`
	DisabledByRoleID    string          `json:"disabled_by_role_id,omitempty"`
	DisableReasonCode   string          `json:"disable_reason_code,omitempty"`
}

// DispatcherAssignment authorizes exactly one execution principal to dispatch a
// model invocation on behalf of a subject role, scoped to one task attempt.
type DispatcherAssignment struct {
	ID                     int64            `json:"id"`
	OrganizationID         string           `json:"organization_id"`
	OrganizationRevisionID int64            `json:"organization_revision_id"`
	TaskID                 int64            `json:"task_id"`
	AttemptID              int64            `json:"attempt_id"`
	SubjectRoleID          string           `json:"subject_role_id"`
	DispatchActorRoleID    string           `json:"dispatch_actor_role_id"`
	ExecutionPrincipalID   int64            `json:"execution_principal_id"`
	Status                 AssignmentStatus `json:"status"`
	ValidFrom              time.Time        `json:"valid_from"`
	ValidUntil             time.Time        `json:"valid_until"`
	MaxInvocations         int              `json:"max_invocations"`
	UsedInvocations        int              `json:"used_invocations"`
	AssignmentHash         string           `json:"assignment_hash"`
	IdempotencyKey         string           `json:"idempotency_key"`
	RequestHash            string           `json:"request_hash"`
	CreatedByRoleID        string           `json:"created_by_role_id"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
	TerminalAt             *time.Time       `json:"terminal_at,omitempty"`
	RevokedAt              *time.Time       `json:"revoked_at,omitempty"`
	RevokedByRoleID        string           `json:"revoked_by_role_id,omitempty"`
	RevocationReasonCode   string           `json:"revocation_reason_code,omitempty"`
}

// AssignmentUse is an immutable, append-only record that a dispatch attempt
// consumed one unit of an assignment's quota. It never stores content.
type AssignmentUse struct {
	ID                   int64     `json:"id"`
	OrganizationID       string    `json:"organization_id"`
	AssignmentID         int64     `json:"assignment_id"`
	InvocationID         int64     `json:"invocation_id"`
	DispatchAttemptID    int64     `json:"dispatch_attempt_id"`
	ExecutionPrincipalID int64     `json:"execution_principal_id"`
	UsedAt               time.Time `json:"used_at"`
	UsageHash            string    `json:"usage_hash"`
}

// ResolvedAssignment is the pinning unit handed to modelruntime when an
// invocation is created: the active assignment plus the principal it names.
type ResolvedAssignment struct {
	Assignment DispatcherAssignment
	Principal  ExecutionPrincipal
}

type RegisterPrincipalCommand struct {
	OrganizationID      string        `json:"organization_id"`
	PrincipalKey        string        `json:"principal_key"`
	DispatchActorRoleID string        `json:"dispatch_actor_role_id"`
	PrincipalKind       PrincipalKind `json:"principal_kind"`
	IdempotencyKey      string        `json:"idempotency_key"`
}

type RegisterPrincipalResult struct {
	Principal ExecutionPrincipal `json:"principal"`
	Reused    bool               `json:"reused"`
}

type CreateAssignmentCommand struct {
	OrganizationID        string
	TaskID                int64
	AttemptID             int64
	SubjectRoleID         string
	ExecutionPrincipalKey string
	MaxInvocations        int
	ValidUntil            *time.Time
	TTL                   *time.Duration
	IdempotencyKey        string
}

type CreateAssignmentResult struct {
	Assignment DispatcherAssignment `json:"assignment"`
	Reused     bool                 `json:"reused"`
}

type ExpireResult struct {
	Expired   int `json:"expired"`
	Inspected int `json:"inspected"`
}

type PreparedRegisterPrincipal struct {
	Command            RegisterPrincipalCommand
	RequestHash        string
	RegisteredByRoleID string
}

type PreparedCreateAssignment struct {
	Command                CreateAssignmentCommand
	Principal              ExecutionPrincipal
	OrganizationRevisionID int64
	ValidFrom              time.Time
	ValidUntil             time.Time
	AssignmentHash         string
	RequestHash            string
	CreatedByRoleID        string
}
