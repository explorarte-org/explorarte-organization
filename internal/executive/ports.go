package executive

import (
	"context"
	"encoding/json"
	"time"
)

type RegistryResolver interface {
	CurrentRevision(context.Context) (RevisionRef, error)
	GetUnit(context.Context, string) (UnitRef, error)
	GetRole(context.Context, string) (RoleRef, error)
	GetLeader(context.Context, string) (RoleRef, error)
}

type TaskCoordinator interface {
	CreateTask(context.Context, CreateTaskCommand) (TaskRecord, bool, error)
	AddDependency(context.Context, int64, int64) error
	GetTask(context.Context, int64) (TaskRecord, error)
	ListByCorrelation(context.Context, string) ([]TaskRecord, error)
	ClaimTask(context.Context, int64, string, string, time.Duration) (TaskRecord, AttemptRecord, LeaseRecord, error)
	StartAttempt(context.Context, LeaseRecord, string) (TaskRecord, error)
	Heartbeat(context.Context, LeaseRecord, string, time.Duration) (LeaseRecord, error)
	RecordAttemptSucceeded(context.Context, LeaseRecord, string, string) (TaskRecord, error)
	RecordAttemptFailed(context.Context, LeaseRecord, string, string, string, bool) (TaskRecord, error)
	RecordEvidence(context.Context, EvidenceCommand) error
	FinalizeCompleted(context.Context, int64, string, string) (TaskRecord, error)
	FinalizeFailed(context.Context, int64, string, string, string, string) (TaskRecord, error)
	BlockTask(context.Context, int64, string, string, string, string) (TaskRecord, error)
	UnblockTask(context.Context, int64, string, string) (TaskRecord, error)
	Reconcile(context.Context, int) error
}

type CreateTaskCommand struct {
	RequestedByRoleID  string
	AssignedRoleID     string
	IdempotencyKey     string
	Title              string
	Instructions       string
	AcceptanceCriteria []string
	Priority           int
	MaxAttempts        int
	CorrelationID      string
	CausationID        string
	Dependencies       []int64
	Requirements       []RequirementProposal
}

type EvidenceCommand struct {
	TaskID        int64
	RequirementID int64
	Type          string
	Reference     string
	Digest        string
	RecordedBy    string
	Metadata      map[string]any
	Satisfies     bool
}

type ContextCoordinator interface {
	Build(context.Context, ContextRequest) (int64, error)
}

type ContextRequest struct {
	OrganizationRevisionID int64
	ActorRoleID            string
	Purpose                string
	TaskRef                string
	IdempotencyKey         string
	CorrelationID          string
	CausationID            string
}

type DispatchProvisioner interface {
	ResolveAssignment(context.Context, int64, int64, string) (AssignmentRef, error)
}

type ModelCoordinator interface {
	EnsureInvocation(context.Context, InvocationCommand) (InvocationRecord, bool, error)
	GetInvocation(context.Context, int64) (InvocationRecord, error)
	FindTaskAttemptInvocations(context.Context, int64, int64) ([]InvocationRecord, error)
	GetResult(context.Context, int64) (InvocationResult, error)
}

type InvocationCommand struct {
	TaskID            int64
	AttemptID         int64
	SubjectRoleID     string
	ContextSnapshotID int64
	Purpose           string
	OutputSchema      json.RawMessage
	MaxOutputTokens   int
	IdempotencyKey    string
	CorrelationID     string
	CausationID       string
	Deadline          time.Time
}

type CompletionGate interface {
	Verify(context.Context, int64, int64) (CompletionResult, error)
}

type AuthorizationGate interface {
	Evaluate(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}

type AuthorizationRequest struct {
	OrganizationRevisionID int64
	ActorRoleID            string
	CapabilityID           string
	ResourceType           string
	ResourceID             string
	ActionDigest           string
	ApprovalRequestID      *int64
}

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
