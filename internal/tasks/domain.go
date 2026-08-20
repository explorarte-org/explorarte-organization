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

// ReasonCodeCoordinationHold marks a task that exists durably but is not yet
// published for execution because the coordination its creator owes it --
// budget inheritance, a delegation edge -- is not durably in place yet.
//
// It is the ONE durable representation of "this task cannot be claimed yet
// for a reason that is nobody's operational problem to clear". Everything
// that decides claimability reads this single fact rather than reimplementing
// the rule:
//
//   - a held task is created blocked, in the same write that creates it, so
//     no window exists in which it is claimable and uncoordinated;
//   - the readiness reconciler promotes blocked tasks only for the three
//     operational reason codes it knows how to resolve, and this is not one
//     of them, so nothing promotes a held task behind its creator's back;
//   - UnblockTask, the operator's tool for clearing an operational block,
//     refuses it, because an operator clearing a dependency block must not
//     be able to publish a child whose coordination never happened;
//   - ReleaseCoordinationHold is the only transition out, and it evaluates
//     dependencies exactly as an unblock does.
//
// The publication barrier and the dependency barrier are different concepts
// and are deliberately not merged: releasing the hold publishes the task,
// which then becomes ready or pending on its dependencies' own merits.
const ReasonCodeCoordinationHold = "awaiting_child_coordination"

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
	// TaskClass describes WHAT KIND OF WORK this durable task represents
	// (M1.3) -- classification metadata only, never authority. Immutable
	// once created, alongside the rest of the task's creation identity.
	// Historical (pre-M1.3) rows read TaskClassLegacyUnspecified.
	TaskClass              string     `json:"task_class"`
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
	// TaskClass is OPTIONAL on the wire: an empty value is defaulted to
	// TaskClassGeneralWork by Service.CreateTask before persistence, never
	// left empty on a newly created row (see ValidTaskClass's doc comment
	// for why "empty" must stay a durable, unambiguous signal reserved for
	// pre-M1.3 historical rows). json:"...,omitempty" is deliberate here:
	// it is what keeps HashCreateRequest's JSON byte-identical to the
	// pre-M1.3 shape whenever TaskClass is empty, which is what makes
	// HashCreateRequestLegacy's compatibility comparison correct (see its
	// doc comment in validation.go).
	TaskClass          string            `json:"task_class,omitempty"`
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
	// HoldForCoordination asks for the task to be created under a
	// coordination hold: durable immediately, claimable only once its
	// creator releases it. See ReasonCodeCoordinationHold.
	//
	// It is excluded from the request hash, and that is a statement about
	// what the hash means rather than a compatibility trick. The request
	// hash exists to catch a contradictory IDENTITY reused under one
	// idempotency key -- a different assignee, different instructions, a
	// different classification of the work. The hold is none of those. It is
	// a host-owned instruction about how to sequence creation, and two
	// requests that differ only in it describe the same task.
	//
	// Including it would also break resume across the deploy that introduces
	// it: a child created before it existed hashes without the field, the
	// resumed caller now always asks for a hold, and neither the current nor
	// the legacy hash would match -- so every in-flight run would die with
	// ErrIdempotencyConflict on its first existing child. Excluding it is
	// both the correct modelling and the reason no such window exists.
	HoldForCoordination bool `json:"-"`
}

type PreparedCreate struct {
	Request                CreateRequest
	OrganizationRevisionID int64
	AssignedUnitID         string
	// TaskClass is the resolved, defaulted, validated value that will
	// actually be persisted -- Request.TaskClass may be empty (caller
	// omitted it); this field never is, for a newly prepared create.
	TaskClass string
	// TaskClassExplicit is true when the ORIGINAL caller request itself
	// supplied a non-empty TaskClass, before Service.CreateTask defaulted
	// an empty one to TaskClassGeneralWork. This distinction matters ONLY
	// for the idempotency-reuse path (Store.Create): "the caller omitted
	// TaskClass" and "the caller explicitly asked for general.work" must
	// never collapse into the same fact once TaskClass is compared
	// against an already-durable row's own known value -- an omission
	// asserts nothing and is compatible with anything; an explicit
	// general.work is a real, specific classification claim like any
	// other and must be compared like one (independent review round 2).
	TaskClassExplicit bool
	RequestHash       string
	InitialStatus Status
	// InitialStatusReasonCode/InitialStatusReason are persisted with the
	// row itself rather than applied afterwards. A hold that had to be
	// applied in a second write would leave the task claimable in between,
	// which is the entire race this exists to close.
	InitialStatusReasonCode string
	InitialStatusReason     string
	DefaultMaxAttempts      int
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

// ClaimRequest carries two deliberately distinct identities.
//
// WorkerID is the operational identity of whatever is doing the work. It is
// recorded on the attempt and as the actor of the task transition, and it is
// meant to stay human-readable ("executive-orchestrator").
//
// HolderPrincipalID is the security identity the lease is issued to. It is the
// value execution authority later matches against the canonical execution
// principal (model_execution_principals.id), so it must be that principal's
// identifier and nothing else. The two were previously collapsed into
// WorkerID, which made a human name double as a security principal.
//
// When HolderPrincipalID is empty the lease falls back to WorkerID. That
// fallback is legacy compatibility for callers that predate this field, and it
// is deliberately NOT the security contract. Anything that executes work under
// Execution Harness authority must set HolderPrincipalID explicitly: the
// fallback issues the lease to an operational name that is not a canonical
// execution principal, and authority will correctly deny every run under it.
type ClaimRequest struct {
	OrganizationID    string        `json:"organization_id,omitempty"`
	WorkerID          string        `json:"worker_id"`
	HolderPrincipalID string        `json:"holder_principal_id,omitempty"`
	AssignedRoleID    string        `json:"assigned_role_id,omitempty"`
	BatchSize         int           `json:"batch_size,omitempty"`
	LeaseDuration     time.Duration `json:"-"`
}

// LeaseHolder is the identity task_leases.holder_id must be issued to, and
// therefore the ActorID every later lease operation on that lease must use.
func (r ClaimRequest) LeaseHolder() string {
	if r.HolderPrincipalID != "" {
		return r.HolderPrincipalID
	}
	return r.WorkerID
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

// ReleaseCoordinationHoldCommand publishes a task that was created held.
//
// It is a distinct operation from UnblockCommand because it answers a
// distinct question. An unblock says "the operational obstacle is gone";
// this says "the coordination I owed this task now exists durably". Only
// the creator can truthfully say the second thing.
type ReleaseCoordinationHoldCommand struct {
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
