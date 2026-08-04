package tasks

import (
	"context"
	"time"
)

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

var allStatuses = map[Status]struct{}{
	StatusPending: {}, StatusReady: {}, StatusLeased: {}, StatusRunning: {},
	StatusAwaitingVerification: {}, StatusBlocked: {}, StatusRetryWait: {},
	StatusCompleted: {}, StatusNoAction: {}, StatusFailed: {}, StatusDeadLetter: {},
	StatusRejected: {}, StatusCancelled: {},
}

func (s Status) Valid() bool { _, ok := allStatuses[s]; return ok }

func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusNoAction, StatusFailed, StatusDeadLetter, StatusRejected, StatusCancelled:
		return true
	default:
		return false
	}
}

type RequirementType string

const (
	RequirementArtifact  RequirementType = "artifact"
	RequirementCheck     RequirementType = "check"
	RequirementApproval  RequirementType = "approval"
	RequirementCondition RequirementType = "condition"
	RequirementResult    RequirementType = "result"
)

func (t RequirementType) Valid() bool {
	switch t {
	case RequirementArtifact, RequirementCheck, RequirementApproval, RequirementCondition, RequirementResult:
		return true
	default:
		return false
	}
}

type RequirementStatus string

const (
	RequirementPending   RequirementStatus = "pending"
	RequirementSatisfied RequirementStatus = "satisfied"
	RequirementFailed    RequirementStatus = "failed"
	RequirementWaived    RequirementStatus = "waived"
)

type AttemptState string

const (
	AttemptLeased       AttemptState = "leased"
	AttemptRunning      AttemptState = "running"
	AttemptFinished     AttemptState = "finished"
	AttemptFailed       AttemptState = "failed"
	AttemptLeaseExpired AttemptState = "lease_expired"
	AttemptCancelled    AttemptState = "cancelled"
)

type LeaseStatus string

const (
	LeaseActive   LeaseStatus = "active"
	LeaseReleased LeaseStatus = "released"
	LeaseExpired  LeaseStatus = "expired"
	LeaseRevoked  LeaseStatus = "revoked"
)

type Task struct {
	ID                     int64      `json:"id"`
	OrganizationID         string     `json:"organization_id"`
	OrganizationRevisionID int64      `json:"organization_revision_id"`
	RequestedByRoleID      *string    `json:"requested_by_role_id,omitempty"`
	AssignedRoleID         string     `json:"assigned_role_id"`
	AssignedUnitID         string     `json:"assigned_unit_id"`
	IdempotencyKey         string     `json:"idempotency_key"`
	RequestHash            string     `json:"request_hash"`
	Title                  string     `json:"title"`
	Instructions           string     `json:"instructions"`
	AcceptanceCriteria     []string   `json:"acceptance_criteria"`
	Status                 Status     `json:"status"`
	Priority               int        `json:"priority"`
	AvailableAt            time.Time  `json:"available_at"`
	MaxAttempts            int        `json:"max_attempts"`
	AttemptCount           int        `json:"attempt_count"`
	Version                int64      `json:"version"`
	CorrelationID          *string    `json:"correlation_id,omitempty"`
	CausationID            *string    `json:"causation_id,omitempty"`
	StatusReasonCode       *string    `json:"status_reason_code,omitempty"`
	StatusReason           *string    `json:"status_reason,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	TerminalAt             *time.Time `json:"terminal_at,omitempty"`
}

type Requirement struct {
	ID          int64             `json:"id"`
	TaskID      int64             `json:"task_id"`
	Key         string            `json:"key"`
	Type        RequirementType   `json:"type"`
	Description string            `json:"description"`
	Required    bool              `json:"required"`
	Status      RequirementStatus `json:"status"`
	SatisfiedAt *time.Time        `json:"satisfied_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type Evidence struct {
	ID            int64           `json:"id"`
	TaskID        int64           `json:"task_id"`
	RequirementID *int64          `json:"requirement_id,omitempty"`
	Type          RequirementType `json:"type"`
	Reference     string          `json:"reference"`
	Digest        *string         `json:"digest,omitempty"`
	RecordedBy    string          `json:"recorded_by"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type Attempt struct {
	ID            int64        `json:"id"`
	TaskID        int64        `json:"task_id"`
	Ordinal       int          `json:"ordinal"`
	State         AttemptState `json:"state"`
	WorkerID      string       `json:"worker_id"`
	ResultSummary *string      `json:"result_summary,omitempty"`
	FailureCode   *string      `json:"failure_code,omitempty"`
	Retryable     *bool        `json:"retryable,omitempty"`
	LeasedAt      time.Time    `json:"leased_at"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	FinishedAt    *time.Time   `json:"finished_at,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

type Lease struct {
	ID            int64       `json:"id"`
	TaskID        int64       `json:"task_id"`
	AttemptID     int64       `json:"attempt_id"`
	HolderID      string      `json:"holder_id"`
	Status        LeaseStatus `json:"status"`
	IssuedAt      time.Time   `json:"issued_at"`
	HeartbeatAt   time.Time   `json:"heartbeat_at"`
	ExpiresAt     time.Time   `json:"expires_at"`
	ReleasedAt    *time.Time  `json:"released_at,omitempty"`
	ReleaseReason *string     `json:"release_reason,omitempty"`
}

type Event struct {
	ID            int64          `json:"id"`
	TaskID        int64          `json:"task_id"`
	Sequence      int64          `json:"sequence"`
	EventType     string         `json:"event_type"`
	FromStatus    *Status        `json:"from_status,omitempty"`
	ToStatus      *Status        `json:"to_status,omitempty"`
	ActorType     string         `json:"actor_type"`
	ActorID       string         `json:"actor_id"`
	CorrelationID *string        `json:"correlation_id,omitempty"`
	CausationID   *string        `json:"causation_id,omitempty"`
	Payload       map[string]any `json:"payload"`
	OccurredAt    time.Time      `json:"occurred_at"`
}

type DeadLetter struct {
	ID            int64      `json:"id"`
	TaskID        int64      `json:"task_id"`
	AttemptID     *int64     `json:"attempt_id,omitempty"`
	ReasonCode    string     `json:"reason_code"`
	Reason        string     `json:"reason"`
	AttemptCount  int        `json:"attempt_count"`
	CreatedAt     time.Time  `json:"created_at"`
	RedrivenAt    *time.Time `json:"redriven_at,omitempty"`
	RedriveTaskID *int64     `json:"redrive_task_id,omitempty"`
}

type OutboxStatus string

const (
	OutboxPending   OutboxStatus = "pending"
	OutboxClaimed   OutboxStatus = "claimed"
	OutboxPublished OutboxStatus = "published"
	OutboxDead      OutboxStatus = "dead"
)

type OutboxEvent struct {
	ID             int64          `json:"id"`
	AggregateType  string         `json:"aggregate_type"`
	AggregateID    string         `json:"aggregate_id"`
	EventType      string         `json:"event_type"`
	SchemaVersion  int            `json:"schema_version"`
	Payload        map[string]any `json:"payload"`
	Status         OutboxStatus   `json:"status"`
	AvailableAt    time.Time      `json:"available_at"`
	AttemptCount   int            `json:"attempt_count"`
	MaxAttempts    int            `json:"max_attempts"`
	ClaimedBy      *string        `json:"claimed_by,omitempty"`
	ClaimExpiresAt *time.Time     `json:"claim_expires_at,omitempty"`
	LastError      *string        `json:"last_error,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	PublishedAt    *time.Time     `json:"published_at,omitempty"`
}

type TaskDetail struct {
	Task         Task          `json:"task"`
	Dependencies []Task        `json:"dependencies"`
	Requirements []Requirement `json:"requirements"`
	Evidence     []Evidence    `json:"evidence"`
	Attempts     []Attempt     `json:"attempts"`
	ActiveLease  *Lease        `json:"active_lease,omitempty"`
}

type RequirementSpec struct {
	Key         string          `json:"key"`
	Type        RequirementType `json:"type"`
	Description string          `json:"description"`
	Required    *bool           `json:"required,omitempty"`
}

type CreateRequest struct {
	OrganizationID     string            `json:"organization_id,omitempty"`
	RequestedByRoleID  string            `json:"requested_by_role_id,omitempty"`
	AssignedRoleID     string            `json:"assigned_role_id"`
	IdempotencyKey     string            `json:"idempotency_key"`
	Title              string            `json:"title"`
	Instructions       string            `json:"instructions"`
	AcceptanceCriteria []string          `json:"acceptance_criteria"`
	Priority           int               `json:"priority,omitempty"`
	AvailableAt        *time.Time        `json:"available_at,omitempty"`
	MaxAttempts        int               `json:"max_attempts,omitempty"`
	CorrelationID      string            `json:"correlation_id,omitempty"`
	CausationID        string            `json:"causation_id,omitempty"`
	Dependencies       []int64           `json:"dependencies,omitempty"`
	Requirements       []RequirementSpec `json:"requirements,omitempty"`
}

type PreparedCreate struct {
	Request                CreateRequest
	OrganizationRevisionID int64
	AssignedUnitID         string
	RequestHash            string
	InitialStatus          Status
	DefaultMaxAttempts     int
	OutboxMaxAttempts      int
	ActorType              string
	ActorID                string
}

type TaskFilter struct {
	OrganizationID string
	Statuses       []Status
	AssignedRoleID string
	AssignedUnitID string
	CorrelationID  string
	Limit          int
	Offset         int
}

type ClaimRequest struct {
	OrganizationID string        `json:"organization_id,omitempty"`
	WorkerID       string        `json:"worker_id"`
	AssignedRoleID string        `json:"assigned_role_id,omitempty"`
	BatchSize      int           `json:"batch_size,omitempty"`
	LeaseDuration  time.Duration `json:"-"`
}

type AssigneeCheck struct {
	Available bool
	Reason    string
}

type AssigneeValidator func(context.Context, Task) (AssigneeCheck, error)

type ClaimedTask struct {
	Task       Task    `json:"task"`
	Attempt    Attempt `json:"attempt"`
	Lease      Lease   `json:"lease"`
	LeaseToken string  `json:"lease_token"`
}

type LeaseCommand struct {
	TaskID     int64
	AttemptID  int64
	LeaseToken string
	ActorID    string
	Extension  time.Duration
}

type AttemptOutcome string

const (
	OutcomeSucceeded           AttemptOutcome = "succeeded"
	OutcomeRetryableFailure    AttemptOutcome = "retryable_failure"
	OutcomeNonRetryableFailure AttemptOutcome = "non_retryable_failure"
	OutcomeCancelled           AttemptOutcome = "cancelled"
)

type AttemptResult struct {
	Outcome     AttemptOutcome `json:"outcome"`
	Summary     string         `json:"summary,omitempty"`
	FailureCode string         `json:"failure_code,omitempty"`
}

type RecordAttemptResultCommand struct {
	LeaseCommand
	Result AttemptResult
}

type FinalOutcome string

const (
	FinalCompleted FinalOutcome = "completed"
	FinalNoAction  FinalOutcome = "no_action"
	FinalFailed    FinalOutcome = "failed"
	FinalRejected  FinalOutcome = "rejected"
	FinalCancelled FinalOutcome = "cancelled"
)

type FinalizeCommand struct {
	TaskID     int64
	Outcome    FinalOutcome
	ReasonCode string
	Reason     string
	ActorType  string
	ActorID    string
}

type BlockCommand struct {
	TaskID      int64
	ReasonCode  string
	Reason      string
	RevokeLease bool
	ActorType   string
	ActorID     string
}

type UnblockCommand struct {
	TaskID    int64
	ActorType string
	ActorID   string
}

type CancelCommand struct {
	TaskID     int64
	ReasonCode string
	Reason     string
	ActorType  string
	ActorID    string
}

type AddDependencyCommand struct {
	TaskID      int64
	DependsOnID int64
	ActorType   string
	ActorID     string
}

type AddRequirementCommand struct {
	TaskID    int64
	Spec      RequirementSpec
	ActorType string
	ActorID   string
}

type RecordEvidenceCommand struct {
	TaskID        int64
	RequirementID *int64
	Type          RequirementType
	Reference     string
	Digest        string
	RecordedBy    string
	Metadata      map[string]any
	Satisfies     bool
}

type RequirementVerificationStatus string

const (
	RequirementVerificationSatisfied RequirementVerificationStatus = "satisfied"
	RequirementVerificationFailed    RequirementVerificationStatus = "failed"
)

type RequirementVerificationCommand struct {
	TaskID        int64
	RequirementID int64
	Result        RequirementVerificationStatus
	Reference     string
	Digest        string
	RecordedBy    string
	Metadata      map[string]any
}

type VerifyExecutionLeaseCommand struct {
	TaskID     int64
	AttemptID  int64
	HolderID   string
	LeaseToken string
}

type ExecutionLeaseContext struct {
	TaskID                 int64
	AttemptID              int64
	OrganizationID         string
	OrganizationRevisionID int64
	AssignedRoleID         string
	AssignedUnitID         string
	TaskStatus             Status
	AttemptState           AttemptState
	HolderID               string
	ExpiresAt              time.Time
}

type ReconcileResult struct {
	ExpiredLeases       int `json:"expired_leases"`
	RecoveredOutbox     int `json:"recovered_outbox"`
	PromotedTasks       int `json:"promoted_tasks"`
	BlockedDependencies int `json:"blocked_dependencies"`
	BlockedAssignees    int `json:"blocked_assignees"`
}

type OutboxClaimRequest struct {
	ConsumerID    string
	BatchSize     int
	ClaimDuration time.Duration
}

type ClaimedOutboxEvent struct {
	Event      OutboxEvent `json:"event"`
	ClaimToken string      `json:"claim_token"`
}

type OutboxDisposition struct {
	EventID    int64
	ClaimToken string
	ConsumerID string
	Error      string
}

type OutboxStats struct {
	Pending   int64 `json:"pending"`
	Claimed   int64 `json:"claimed"`
	Published int64 `json:"published"`
	Dead      int64 `json:"dead"`
}

type RoleRef struct {
	ID         string
	UnitID     string
	Enabled    bool
	Executable bool
	Retired    bool
}

type RevisionRef struct {
	ID int64
}
