package executive

import (
	"context"
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
	ClaimTask(context.Context, ClaimTaskCommand) (TaskRecord, AttemptRecord, LeaseRecord, error)
	StartAttempt(context.Context, LeaseRecord, string) (TaskRecord, error)
	Heartbeat(context.Context, LeaseRecord, string, time.Duration) (LeaseRecord, error)
	RecordAttemptSucceeded(context.Context, LeaseRecord, string, string) (TaskRecord, error)
	RecordAttemptFailed(context.Context, LeaseRecord, string, string, string, bool) (TaskRecord, error)
	RecordEvidence(context.Context, EvidenceCommand) error
	FinalizeCompleted(context.Context, int64, string, string) (TaskRecord, error)
	FinalizeFailed(context.Context, int64, string, string, string, string) (TaskRecord, error)
	BlockTask(context.Context, int64, string, string, string, string) (TaskRecord, error)
	UnblockTask(context.Context, int64, string, string) (TaskRecord, error)
	// ReleaseCoordinationHold publishes a task created with
	// HoldForCoordination once its creator's obligations are durable. It is
	// separate from UnblockTask because only the creator can truthfully
	// assert that coordination happened, and it is idempotent: releasing an
	// already-published task reports it unchanged.
	ReleaseCoordinationHold(context.Context, int64) (TaskRecord, error)
	Reconcile(context.Context, int) error
}

// ClaimTaskCommand carries the two identities a claim needs, as separate
// fields, because they are separate things.
//
// WorkerID is operational provenance: which process is doing the work. It is
// recorded on the attempt and reads as a name ("executive-orchestrator").
//
// HolderPrincipalID is the security identity the lease is issued to, and
// therefore the ActorID every later lease-authorized mutation on that lease
// must present. It is the canonical role-bound execution principal for
// AssignedRoleID. Passing a worker name here would issue a lease to something
// that is not an execution principal, and the Harness would then correctly
// deny every run under it -- which is why these are two fields and not one.
type ClaimTaskCommand struct {
	TaskID            int64
	WorkerID          string
	HolderPrincipalID string
	AssignedRoleID    string
	LeaseDuration     time.Duration
}

// ExecutionPrincipalRef is the whole of what the Executive needs to know about
// an execution principal: an identifier it can put in a lease, an ActorID and
// a run identity, plus the role it is bound to so a caller can prove the
// binding it asked for is the binding it got. Nothing about keys, kinds,
// status transitions or provisioning crosses this boundary.
type ExecutionPrincipalRef struct {
	// ID is the canonical identifier of model_execution_principals.id, in the
	// exact string form that task_leases.holder_id stores.
	ID string
	// RoleID is the role the principal is bound to (dispatch_actor_role_id).
	RoleID string
}

// RoleBoundPrincipalResolver resolves the single active execution principal
// bound to an already-persisted, already-registry-validated AssignedRoleID.
//
// It exists so the Executive depends on one narrow verb instead of the whole
// dispatch principal store: the Executive must be able to ask "who executes
// for this role", and must not be able to disable, list, or re-key principals.
type RoleBoundPrincipalResolver interface {
	ResolveRoleBoundPrincipal(context.Context, string) (ExecutionPrincipalRef, error)
}

type CreateTaskCommand struct {
	RequestedByRoleID string
	AssignedRoleID    string
	// TaskClass is OPTIONAL: empty is defaulted by the Tasks Engine to
	// its own safe generic class. When non-empty it is validated by the
	// HOST (see ValidTaskClass) before it ever reaches persistence --
	// never trusted merely because a Leader proposed it (M1.3 section 5).
	TaskClass          string
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
	// HoldForCoordination creates the task under a durable publication
	// barrier instead of ready. It is set by coordinatedChildren and by
	// nothing else: a caller free to omit it would be a caller free to
	// recreate the race it closes.
	HoldForCoordination bool
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

// ContextCoordinator builds the context snapshot one cognitive execution runs
// against. It returns the whole snapshot reference, not just its ID, because
// the Harness run identity binds the context bytes: Model Runtime requires the
// invocation's stable prefix to be the byte-exact render of the referenced
// snapshot, so the Executive has to carry the render, not a pointer to it.
type ContextCoordinator interface {
	Build(context.Context, ContextRequest) (ContextSnapshot, error)
}

// ContextSnapshot is a durable, already-rendered context snapshot.
type ContextSnapshot struct {
	ID      int64
	Version string
	// Digest is the SHA-256 of Content, in hex. The Harness re-derives and
	// compares it, so a snapshot whose render drifted from its hash cannot
	// enter a run.
	Digest  string
	Content string
	// ExecutionContextViewID is the durable identity of the persisted,
	// immutable derived view Content/Digest came from (M1.1,
	// internal/contextcompiler.ExecutionContextView). Zero when the
	// coordinator does not persist a durable view. Model Runtime resolves
	// and persists the identical view for the same canonical snapshot
	// through the same store, so this ID is shared, not independently
	// reconstructed.
	ExecutionContextViewID int64
}

// ModelBudgetGate is the Executive's own correlation-wide model-call budget.
//
// It is a gate rather than a decorator on invocation creation because the
// Executive no longer creates invocations: the Harness does, inside Model
// Runtime. Enforcing the limit at the old place would mean enforcing it on a
// path the productive Executive can no longer reach, i.e. not enforcing it.
type ModelBudgetGate interface {
	// AuthorizeModelCall reports whether the correlation can afford the model
	// call this attempt is about to make. It must count durable state, never
	// an in-memory tally, and must not charge an attempt that already has an
	// invocation -- a resumed run is the same logical call.
	AuthorizeModelCall(context.Context, ModelCallBudgetRequest) error
}

type ModelCallBudgetRequest struct {
	TaskID        int64
	AttemptID     int64
	CorrelationID string
}

type ContextRequest struct {
	OrganizationRevisionID int64
	ActorRoleID            string
	Purpose                string
	TaskRef                string
	// TaskClass/ExecutionPurpose/ActorUnitID are M1.3's durable semantic
	// selector facts. ExecutionPurpose here is the semantic enum string
	// (string(purpose)), deliberately distinct from the legacy Purpose
	// field above (purpose.LegacyPurpose()) -- Context Engine/Model
	// Runtime compatibility still depends on Purpose staying byte-
	// identical to what it always was.
	TaskClass        string
	ExecutionPurpose string
	ActorUnitID      string
	// RepositoryBaseSHA is the campaign's pinned commit, set only for
	// executions that are allowed to observe code. It comes from the durable
	// pin and never from resolving the promotion target again: a context
	// assembled against a freshly resolved head would show one round a
	// different repository than the last.
	RepositoryBaseSHA string
	// RepositorySubjects are the symbols this execution is OBLIGED to
	// ground. They are searched before anything derived from prose: an
	// obligation is a typed fact, and re-discovering it from the goal's
	// wording would put it back in competition for the same budget it
	// already lost once.
	RepositorySubjects []string
	// RepositorySlots are those same obligations at their full normative
	// resolution: (subject, relation) pairs, checkpoint D. Subjects alone
	// lost the relation before selection ever ran -- R15's round 2 demanded
	// driveDesignFreeze/application and received only its declaration -- so
	// the slot list travels intact from the durable requirements through the
	// context engine into mandatory-first selection.
	RepositorySlots []EvidenceSlot
	// RepositoryQuery is what the selection reads to decide where to look.
	RepositoryQuery string
	IdempotencyKey  string
	CorrelationID   string
	CausationID     string
}

type DispatchProvisioner interface {
	EnsureAuthorizedAssignmentForRunningAttempt(context.Context, int64, int64) (AssignmentRef, error)
	ResolveAssignment(context.Context, int64, int64, string) (AssignmentRef, error)
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
	// CreateRootBudget starts the campaign's budget tree at the ceilings the
	// submission resolved. The limits are a parameter, not provider state:
	// a provider that carried its own would be a second answer to what a
	// campaign may spend, and the durable row keeps whichever answer arrived
	// first.
	CreateRootBudget(ctx context.Context, root TaskRecord, limits CampaignBudget, now time.Time) error
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
// CRITICAL FIX 6 (extended by EXEC-PRINCIPAL-001): every send is
// authenticated by an execution principal bound to sender.AssignedRoleID.
// There is no caller-supplied principal parameter — a static
// executionPrincipalID cannot authenticate a multi-hop flow where each hop
// has a different sender role (CEO->leader, leader->worker, worker->leader,
// leader->CEO), so the implementation resolves the principal itself,
// server-side, from sender.AssignedRoleID (see
// runtimeadapter.AgentMessages.resolveOrProvisionPrincipalForRole). This
// principal must have dispatch_actor_role_id == sender.AssignedRoleID for
// send authentication, and ClaimNext/Ack/Nack require matching principals
// for inbox access and settlement.
type AgentMessagingProvider interface {
	SendDelegation(ctx context.Context, sender, recipient TaskRecord, now time.Time) error
	SendCompletion(ctx context.Context, sender, recipient TaskRecord, now time.Time) error
}

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
