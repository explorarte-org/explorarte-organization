package modelruntime

import (
	"context"
	"time"
)

type OrganizationRef struct {
	ID               string
	RevisionID       int64
	ModelRoutingHash string
}
type RoleRef struct {
	ID             string
	ModelPolicy    string
	Enabled        bool
	Executable     bool
	AuthorityClass string
	UnitID         string
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
type ContextSnapshotRef struct {
	ID                     int64
	OrganizationID         string
	OrganizationRevisionID int64
	ActorRoleID            string
	TaskRef                string
	Status                 string
	RenderedHash           string
	DataClasses            []string
}
type AuthorizationDecision struct {
	Allowed    bool
	ReasonCode string
}

type OrganizationCatalog interface {
	CurrentOrganization(context.Context, string) (OrganizationRef, error)
	GetRole(context.Context, string, string) (RoleRef, error)
	ListRoles(context.Context, string) ([]RoleRef, error)
}

type TaskAttemptReader interface {
	GetTaskAttempt(context.Context, int64, int64) (TaskAttemptRef, error)
}
type ContextReader interface {
	GetContextSnapshot(context.Context, int64) (ContextSnapshotRef, error)
	ValidateContextSnapshot(context.Context, int64) error
	RenderContextSnapshot(context.Context, int64) ([]byte, error)
}
type CapabilityEvaluator interface {
	EvaluateDispatch(context.Context, string, int64, string, string, string) (AuthorizationDecision, error)
}

type ProviderAdapter interface {
	ProviderID() string
	Dispatch(context.Context, CanonicalRequest) (RawResponse, error)
}
type AdapterRegistry interface {
	Get(string) (ProviderAdapter, bool)
}

type RegistryStore interface {
	RecordRegistryValidated(context.Context, string, string) error
	RegistryStatus(context.Context, string, int64, string) (RegistryStatus, error)
	ApplyRegistry(context.Context, RegistryPlan, int) (RegistrySyncResult, error)
	GetBinding(context.Context, string, int64, string) (ResolvedBinding, error)
}

type InvocationStore interface {
	CreateInvocation(context.Context, PreparedInvocation, int) (CreateInvocationResult, error)
	GetInvocation(context.Context, int64) (Invocation, error)
	ListInvocations(context.Context, string, int) ([]Invocation, error)
	ClaimInvocation(context.Context, ClaimCommand, RuntimeConfig) (ClaimedInvocation, error)
	MarkSendStarted(context.Context, int64, int64, string, string) (Invocation, error)
	MarkResponseReceived(context.Context, int64, int64, string, string) (Invocation, error)
	CompleteInvocation(context.Context, CompletionCommand, int) (DispatchResult, error)
	FailBeforeSend(context.Context, FailureCommand, int) (Invocation, error)
	FailAfterResponse(context.Context, FailureCommand, int) (Invocation, error)
	MarkAmbiguous(context.Context, FailureCommand, string, int) (Invocation, error)
	RequestCancellation(context.Context, int64, string, string, int) (CancelResult, error)
	CancellationRequested(context.Context, int64) (bool, error)
	MarkCancelled(context.Context, FailureCommand, int) (Invocation, error)
	Reconcile(context.Context, string, int, int) (ReconcileResult, error)
	WatchCancellation(context.Context, int64) error
}

type Store interface {
	RegistryStore
	InvocationStore
}

type PreparedInvocation struct {
	Command                CreateInvocationCommand
	OrganizationRevisionID int64
	Binding                ResolvedBinding
	RequestHash            string
	RequiredCapabilities   []ModelCapability
	OutputSchema           []byte
}
type ClaimCommand struct {
	InvocationID int64
	ClaimedBy    string
}
type CompletionCommand struct {
	InvocationID      int64
	DispatchAttemptID int64
	ClaimToken        string
	Response          NormalizedResponse
}
type FailureCommand struct {
	InvocationID          int64
	DispatchAttemptID     int64
	ClaimToken            string
	ErrorCode             string
	OutcomeClassification string
	EventType             string
}

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
