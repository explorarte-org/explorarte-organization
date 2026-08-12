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
	// ListAwaitingGating returns tasks whose latest attempt finished but
	// were never finalized by gatedComplete — the crash window
	// ReconcileGatedCompletions exists to close. A task sits in this exact
	// status from the moment its attempt finishes until gatedComplete
	// successfully finalizes/blocks it; nothing else produces this status.
	ListAwaitingGating(context.Context, int) ([]TaskRecord, error)
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

// DecisionRecorder durably records the completion verdict for one finished
// task attempt as a decision-graph trace (internal/decisiongraph), so
// internal/evaluation and internal/improvement have a real trace to act on
// instead of the orchestrator's decision leaving no evidence behind.
type DecisionRecorder interface {
	RecordAttemptDecision(context.Context, AttemptDecisionRecord) error
}

type AttemptDecisionRecord struct {
	TaskID    int64
	AttemptID int64
	Verdict   CompletionVerdict
	Detail    string
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

// AgentBudgetProvider creates and inherits multidimensional budgets across
// an execution tree. It is optional on Orchestrator (see
// WithAgentBudgets) — a nil provider means no budget tracking, existing
// behavior is unaffected.
type AgentBudgetProvider interface {
	CreateRootBudget(ctx context.Context, root TaskRecord, now time.Time) error
	// InheritForChild attaches child to root's budget tree at depth. It
	// shares root's remaining budget outright — it never carves out a
	// separate sub-allocation, since the orchestrator has no per-child
	// limits of its own to hand out.
	InheritForChild(ctx context.Context, root, child TaskRecord, depth int64, now time.Time) error
}

// AgentMessagingProvider sends durable delegation/completion messages
// between agents (CEO<->coordinador, coordinador<->worker). It is optional
// on Orchestrator (see WithAgentMessaging) — a nil provider means no
// messaging, existing behavior is unaffected.
//
// CRITICAL FIX 6: All operations require an authenticated execution principal.
// There is NO fallback to free-string consumerIDs in production.
// SendDelegation and SendCompletion receive executionPrincipalID as the first
// parameter after context. This principal must have dispatch_actor_role_id ==
// sender.AssignedRoleID for send authentication, and ClaimNext/Ack/Nack
// require matching principals for inbox access and settlement.
type AgentMessagingProvider interface {
	SendDelegation(ctx context.Context, executionPrincipalID string, sender, recipient TaskRecord, now time.Time) error
	SendCompletion(ctx context.Context, executionPrincipalID string, sender, recipient TaskRecord, now time.Time) error
}

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
