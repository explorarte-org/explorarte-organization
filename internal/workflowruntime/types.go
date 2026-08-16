// Package workflowruntime is the V2 application boundary for organizational
// work. It composes the durable task lifecycle, completion verification,
// decision branching, and authorized cross-role coordination without owning a
// second state machine or persistence model.
package workflowruntime

import "time"

type Status string

const (
	StatusPending              Status = "pending"
	StatusReady                Status = "ready"
	StatusLeased               Status = "leased"
	StatusRunning              Status = "running"
	StatusAwaitingVerification Status = "awaiting_verification"
	StatusBlocked              Status = "blocked"
	StatusRetryWait            Status = "retry_wait"
	StatusCompleted            Status = "completed"
	StatusNoAction             Status = "no_action"
	StatusFailed               Status = "failed"
	StatusDeadLetter           Status = "dead_letter"
	StatusRejected             Status = "rejected"
	StatusCancelled            Status = "cancelled"
)

func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusNoAction, StatusFailed, StatusDeadLetter, StatusRejected, StatusCancelled:
		return true
	default:
		return false
	}
}

type Actor struct {
	OrganizationID       string
	RoleID               string
	ExecutionPrincipalID string
	ActorType            string
	ActorID              string
}

func (a Actor) DurableActorID() string {
	if a.ActorID != "" {
		return a.ActorID
	}
	return a.ExecutionPrincipalID
}

type Requirement struct {
	ID          int64
	Key         string
	Type        string
	Description string
	Required    bool
	Status      string
}

type RequirementSpec struct {
	Key         string
	Type        string
	Description string
	Required    bool
}

type Evidence struct {
	ID            int64
	RequirementID int64
	Type          string
	Reference     string
	Digest        string
	RecordedBy    string
	Metadata      map[string]any
	CreatedAt     time.Time
}

type Attempt struct {
	ID      int64
	Ordinal int
	State   string
}

type DurableEvent struct {
	ID            int64
	Sequence      int64
	EventType     string
	FromStatus    Status
	ToStatus      Status
	ActorType     string
	ActorID       string
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
}

type CompletionDisposition string

const (
	CompletionUnchecked CompletionDisposition = "unchecked"
	CompletionAllow     CompletionDisposition = "allow_complete"
	CompletionDeny      CompletionDisposition = "deny_complete"
	CompletionBlocked   CompletionDisposition = "blocked_unsatisfied"
)

type CompletionDecision struct {
	TaskID      int64
	AttemptID   int64
	Disposition CompletionDisposition
	Reason      string
	Provenance  string
}

type Snapshot struct {
	TaskID            int64
	OrganizationID    string
	Status            Status
	AssignedRoleID    string
	AssignedUnitID    string
	RequestedByRoleID string
	CorrelationID     string
	CausationID       string
	Requirements      []Requirement
	Evidence          []Evidence
	Attempts          []Attempt
	Completion        CompletionDecision
	Events            []DurableEvent
	LastTransition    *DurableEvent
}

type WorkRequest struct {
	OrganizationID     string
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
	Requirements       []RequirementSpec
}

type InitiateCommand struct {
	Actor Actor
	Work  WorkRequest
}

type TaskAccess string

const (
	TaskAccessObserve TaskAccess = "observe"
	TaskAccessMutate  TaskAccess = "mutate"
)

type ObserveCommand struct {
	Actor  Actor
	TaskID int64
}

type ExecutionCommand struct {
	Actor         Actor
	TaskID        int64
	AttemptID     int64
	LeaseToken    string
	CorrelationID string
	CausationID   string
}

type Outcome string

const (
	OutcomeSucceeded           Outcome = "succeeded"
	OutcomeRetryableFailure    Outcome = "retryable_failure"
	OutcomeNonRetryableFailure Outcome = "non_retryable_failure"
	OutcomeCancelled           Outcome = "cancelled"
)

type OutcomeCommand struct {
	ExecutionCommand
	Outcome     Outcome
	Summary     string
	FailureCode string
}

type EvidenceCommand struct {
	Actor         Actor
	TaskID        int64
	RequirementID int64
	Type          string
	Reference     string
	Digest        string
	Metadata      map[string]any
	Satisfies     bool
	CorrelationID string
	CausationID   string
}

type CompleteCommand struct {
	Actor         Actor
	TaskID        int64
	AttemptID     int64
	CorrelationID string
	CausationID   string
}

type CompleteResult struct {
	Decision CompletionDecision
	Snapshot Snapshot
}

type CoordinationKind string

const (
	CoordinationDelegation CoordinationKind = "delegation"
	CoordinationCompletion CoordinationKind = "completion"
)

type CoordinationCommand struct {
	Actor           Actor
	OrganizationID  string
	SenderTaskID    int64
	RecipientTaskID int64
	CorrelationID   string
	CausationID     string
	Kind            CoordinationKind
	IdempotencyKey  string
}

type CoordinationRecord struct {
	ID                int64
	OrganizationID    string
	SenderRoleID      string
	SenderTaskID      int64
	RecipientRoleID   string
	RecipientTaskID   int64
	CorrelationID     string
	CausationID       string
	Kind              CoordinationKind
	IdempotencyKey    string
	DurableProvenance string
}

type BranchActionKind string

const BranchActionBlock BranchActionKind = "block_task"

type BranchAction struct {
	Kind       BranchActionKind
	ReasonCode string
	Reason     string
}

type BranchRequest struct {
	Actor         Actor
	TaskID        int64
	AttemptID     int64
	CorrelationID string
	CausationID   string
	InputRef      string
}

type BranchDecision struct {
	TaskID         int64
	CorrelationID  string
	CausationID    string
	SelectedBranch string
	DecisionRef    string
	Action         BranchAction
}

type GoalRequest struct {
	Actor              Actor
	Goal               string
	AcceptanceCriteria []string
	Requirements       []RequirementSpec
	IdempotencyKey     string
}

type ExecutiveStart struct {
	RootTaskID    int64
	CorrelationID string
	LegacyState   string
	Reused        bool
}

type WorkflowStart struct {
	Executive ExecutiveStart
	Snapshot  Snapshot
}
