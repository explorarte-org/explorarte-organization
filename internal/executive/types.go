package executive

import (
	"encoding/json"
	"time"
)

const (
	ExecutivePlanSchemaVersion    = "executive-plan/v1"
	DepartmentPlanSchemaVersion   = "department-plan/v1"
	DepartmentReviewSchemaVersion = "department-review/v1"
	ExecutiveClosureSchemaVersion = "executive-closure/v1"

	OwnerRoleID    = "empresa/human"
	CEORoleID      = "empresa/ceo"
	ObserverRoleID = "empresa/ceo_observer"
)

type RunState string

const (
	StateAccepted               RunState = "accepted"
	StateCEOPlanning            RunState = "ceo_planning"
	StateDepartmentPlanning     RunState = "department_planning"
	StateWorkerExecution        RunState = "worker_execution"
	StateDepartmentReview       RunState = "department_review"
	StateCompletionVerification RunState = "completion_verification"
	StateCEOClosure             RunState = "ceo_closure"
	StateCompleted              RunState = "completed"
	StateBlocked                RunState = "blocked"
	StateFailed                 RunState = "failed"
	StateCancelled              RunState = "cancelled"
	StateInconclusive           RunState = "inconclusive"
)

func (s RunState) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

type ExecutivePlan struct {
	SchemaVersion          string              `json:"schema_version"`
	Objective              string              `json:"objective"`
	DepartmentRequests     []DepartmentRequest `json:"department_requests"`
	GlobalConstraints      []string            `json:"global_constraints"`
	SuccessCriteria        []string            `json:"success_criteria"`
	OwnerDecisionsRequired []string            `json:"owner_decisions_required"`
}

type DepartmentRequest struct {
	UnitID      string   `json:"unit_id"`
	Objective   string   `json:"objective"`
	Deliverable string   `json:"deliverable"`
	Priority    int      `json:"priority"`
	Constraints []string `json:"constraints"`
}

type DepartmentPlan struct {
	SchemaVersion  string               `json:"schema_version"`
	DepartmentID   string               `json:"department_id"`
	Tasks          []WorkerTaskProposal `json:"tasks"`
	ReviewCriteria []string             `json:"review_criteria"`
	Unresolved     []string             `json:"unresolved"`
}

type WorkerTaskProposal struct {
	ClientKey      string `json:"client_key"`
	AssignedRoleID string `json:"assigned_role_id"`
	// TaskClass is proposed by the Leader but HOST-VALIDATED before it
	// ever reaches CreateTaskCommand (M1.3 section 5, see
	// validateWorkerTaskShape). Missing on pre-M1.3 durable output
	// (recovered/replayed): defaults to TaskClassGeneralWork, never
	// TaskClassOf(role) -- that proxy is not reintroduced to "fix" old
	// outputs.
	TaskClass          string                `json:"task_class,omitempty"`
	Title              string                `json:"title"`
	Instructions       string                `json:"instructions"`
	AcceptanceCriteria []string              `json:"acceptance_criteria"`
	Dependencies       []string              `json:"dependencies"`
	Requirements       []RequirementProposal `json:"requirements"`
	Priority           int                   `json:"priority"`
}

type RequirementProposal struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type DepartmentReview struct {
	SchemaVersion         string               `json:"schema_version"`
	Verdict               ReviewVerdict        `json:"verdict"`
	Findings              []string             `json:"findings"`
	UnsatisfiedCriteria   []string             `json:"unsatisfied_criteria"`
	EvidenceRefs          []string             `json:"evidence_refs"`
	ProposedFollowupTasks []WorkerTaskProposal `json:"proposed_followup_tasks"`
}

type ReviewVerdict string

const (
	ReviewAccept      ReviewVerdict = "accept"
	ReviewNeedsReplan ReviewVerdict = "needs_replan"
	ReviewBlocked     ReviewVerdict = "blocked"
	ReviewFail        ReviewVerdict = "fail"
)

type ExecutiveClosure struct {
	SchemaVersion       string        `json:"schema_version"`
	Status              ClosureStatus `json:"status"`
	AnswerToOwner       string        `json:"answer_to_owner"`
	CompletedItems      []string      `json:"completed_items"`
	BlockedItems        []string      `json:"blocked_items"`
	UnresolvedDecisions []string      `json:"unresolved_decisions"`
	EvidenceRefs        []string      `json:"evidence_refs"`
}

type ClosureStatus string

const (
	ClosureCompleted ClosureStatus = "completed"
	ClosurePartial   ClosureStatus = "partial"
	ClosureBlocked   ClosureStatus = "blocked"
	ClosureFailed    ClosureStatus = "failed"
)

type Limits struct {
	MaxInputBytes          int
	MaxDepartments         int
	MaxWorkerTasksPerPlan  int
	MaxFollowupTasks       int
	MaxArrayItems          int
	MaxStringBytes         int
	MaxInstructionsBytes   int
	MaxAcceptanceCriteria  int
	MaxRequirementsPerTask int
	MaxDepartmentReplans   int
	MaxModelCalls          int
	MaxOutputTokens        int
	InvocationDeadline     time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes: 256 << 10, MaxDepartments: 7, MaxWorkerTasksPerPlan: 24,
		MaxFollowupTasks: 12, MaxArrayItems: 64, MaxStringBytes: 4000,
		MaxInstructionsBytes: 16000, MaxAcceptanceCriteria: 32, MaxRequirementsPerTask: 32,
		MaxDepartmentReplans: 1, MaxModelCalls: 128, MaxOutputTokens: 8192,
		InvocationDeadline: 10 * time.Minute,
	}
}

type OwnerGoal struct {
	Goal               string                `json:"goal"`
	AcceptanceCriteria []string              `json:"acceptance_criteria"`
	Requirements       []RequirementProposal `json:"requirements,omitempty"`
}

type SubmitRequest struct {
	Goal           OwnerGoal
	ActorRoleID    string
	IdempotencyKey string
}

type Run struct {
	RootTaskID    int64     `json:"root_task_id"`
	CorrelationID string    `json:"correlation_id"`
	State         RunState  `json:"state"`
	ReasonCode    string    `json:"reason_code,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	AnswerToOwner string    `json:"answer_to_owner,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type TaskRecord struct {
	ID                     int64
	OrganizationID         string
	OrganizationRevisionID int64
	RequestedByRoleID      string
	AssignedRoleID         string
	AssignedUnitID         string
	TaskClass              string
	IdempotencyKey         string
	RequestHash            string
	Title                  string
	Instructions           string
	AcceptanceCriteria     []string
	Status                 string
	Priority               int
	MaxAttempts            int
	AttemptCount           int
	CorrelationID          string
	CausationID            string
	ReasonCode             string
	Reason                 string
	Requirements           []RequirementRecord
	Evidence               []EvidenceRecord
	Attempts               []AttemptRecord
	ActiveLease            *LeaseRecord
}

type RequirementRecord struct {
	ID       int64
	Key      string
	Type     string
	Required bool
	Status   string
}

type EvidenceRecord struct {
	ID            int64
	RequirementID int64
	Type          string
	Reference     string
	Digest        string
	RecordedBy    string
	Metadata      map[string]any
}

type AttemptRecord struct {
	ID            int64
	Ordinal       int
	State         string
	ResultSummary string
	FailureCode   string
}

type LeaseRecord struct {
	TaskID     int64
	AttemptID  int64
	HolderID   string
	LeaseToken string
	ExpiresAt  time.Time
}

type InvocationRecord struct {
	ID            int64
	TaskID        int64
	AttemptID     int64
	SubjectRoleID string
	Status        string
	ErrorCode     string
	CorrelationID string
	CausationID   string
	// ProviderExecutionMayHaveStarted is Model Runtime's own answer to "may
	// this request already have reached the provider, without a resolved
	// outcome yet". The Executive stores the answer rather than the rule: the
	// states on either side of that line belong to Model Runtime, and copying
	// them here would create a second state machine that could drift from the
	// one that actually reconciles.
	ProviderExecutionMayHaveStarted bool
}

type InvocationResult struct {
	InvocationID  int64
	JSONOutput    json.RawMessage
	TextOutput    string
	ToolIntents   int
	ResponseHash  string
	ResponseBytes int
}

type CompletionVerdict string

const (
	CompletionPass         CompletionVerdict = "pass"
	CompletionFail         CompletionVerdict = "fail"
	CompletionInconclusive CompletionVerdict = "inconclusive"
)

type CompletionResult struct {
	Verdict CompletionVerdict
	Detail  string
}

type UnitRef struct {
	ID           string
	Operational  bool
	Leaderless   bool
	LeaderRoleID string
	Retired      bool
}

type RoleRef struct {
	ID              string
	UnitID          string
	Enabled         bool
	Executable      bool
	Retired         bool
	CanonicalLeader bool
	AuthorityClass  string
}

type RevisionRef struct {
	ID            int64
	CanonicalHash string
}

type AssignmentRef struct {
	ID                     int64
	OrganizationRevisionID int64
	TaskID                 int64
	AttemptID              int64
	SubjectRoleID          string
	ExecutionPrincipalID   int64
	DispatchActorRoleID    string
	ValidUntil             time.Time
}

type AuthorizationDecision struct {
	Allowed    bool
	Effect     string
	ReasonCode string
}
