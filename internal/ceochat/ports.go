package ceochat

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// Store is the durable conversation/message persistence port. The
// PostgreSQL implementation lives in internal/ceochat/postgres; tests use an
// in-memory fake that enforces the same idempotency contract.
type Store interface {
	CreateConversation(ctx context.Context, conversation Conversation) (Conversation, error)
	GetConversation(ctx context.Context, organizationID string, id int64) (Conversation, error)
	// AppendMessage inserts one message and assigns its Sequence. For an
	// owner message it enforces UNIQUE(conversation_id, idempotency_key) and
	// returns ErrIdempotencyConflict-classified information through
	// FindOwnerMessage rather than through this call's own error -- callers
	// are expected to call AppendMessage, and on a unique-key collision fall
	// back to FindOwnerMessage to decide reuse vs conflict. See
	// postgres.Store.AppendMessage for the exact detection.
	AppendMessage(ctx context.Context, message Message) (Message, error)
	// FindOwnerMessage returns the owner message previously recorded under
	// this exact (conversation, idempotency_key), if any.
	FindOwnerMessage(ctx context.Context, conversationID int64, idempotencyKey string) (Message, bool, error)
	// FindAssistantReply returns the assistant message, if any, that was
	// recorded in response to the given owner message.
	FindAssistantReply(ctx context.Context, ownerMessageID int64) (Message, bool, error)
	// ListMessages returns up to limit of the most recent messages in a
	// conversation, oldest first (the order a transcript reads in).
	ListMessages(ctx context.Context, conversationID int64, limit int) ([]Message, error)
}

// PrincipalResolver resolves the real, active, role-bound execution
// principal for a role. It is never satisfied with a fabricated or
// zero-value identifier: ceochat carries no authority of its own and
// borrows exactly the same identity the Executive Harness authorizes
// against.
type PrincipalResolver interface {
	Resolve(ctx context.Context, roleID string) (principalID string, err error)
}

// ContextSnapshot is the byte-exact, provider-visible context a chat turn's
// Harness run is bound to. ID/Version/Digest/Content mirror
// executionharness.InitialContext's fields exactly (ceochat does not import
// internal/executive, so it does not reuse executive.ContextSnapshot).
type ContextSnapshot struct {
	ID      int64
	Version string
	Digest  string
	Content string
}

// ContextRequest asks the Context Engine to build (or reuse) the snapshot a
// chat turn's Harness run will be bound to.
type ContextRequest struct {
	ActorRoleID            string
	OrganizationRevisionID int64
	TaskRef                string
	CorrelationID          string
	CausationID            string
	IdempotencyKey         string
}

// ContextBuilder is the minimal seam ceochat needs from the Context Engine.
// The production implementation (internal/ceochat/bootstrap) composes it
// from contextengine.Service + contextcompiler.ContextAssemblyService, the
// same two seams internal/executive/runtimeadapter.Context uses -- there is
// no bypass to a provider and no ad-hoc prompt construction.
type ContextBuilder interface {
	Build(ctx context.Context, request ContextRequest) (ContextSnapshot, error)
}

// DispatchProvisioner ensures a bounded Model Dispatch authorization exists
// for a running task attempt before the Harness is allowed to invoke the
// model. It is the seam CEO_CONVERSATIONAL_REAL_PROVIDER_REHEARSAL_V1 found
// missing: without it, Model Runtime's InvocationService.Create fails
// closed with modeldispatch.ErrNotFound on the very first invocation --
// ceochat claimed the task and started the attempt, but nothing had ever
// provisioned the modeldispatch.DispatcherAssignment that
// InvocationService.Create's ResolveActive call requires.
//
// The production implementation (internal/ceochat/bootstrap) wraps a
// *modeldispatch.AuthorizedAttemptProvisioner constructed with
// modeldispatch.WithMaxInvocations(MaxTurns): a chat turn's Harness run may
// make up to MaxTurns model invocations within the same task attempt (one
// per tool-calling round), all needing the same assignment -- unlike
// Executive's typed-task profile, which never leaves the default quota of
// 1. The quota is fixed at construction time, entirely host-owned: nothing
// this interface exposes lets an owner message, model output, tool
// argument, or task instruction choose how many invocations an attempt is
// allowed.
//
// EnsureAuthorizedAssignmentForRunningAttempt is idempotent for the same
// running attempt (repeated calls resolve the same assignment rather than
// creating a second one) and deliberately returns no assignment detail --
// ceochat only needs to know whether a call is now authorized to proceed to
// the Harness, not the assignment's own identity or quota.
type DispatchProvisioner interface {
	EnsureAuthorizedAssignmentForRunningAttempt(ctx context.Context, taskID, attemptID int64) error
}

// TaskCoordinator is the narrow slice of the Task Engine a chat turn needs:
// create-or-reuse the turn's task, read its current state, claim it exactly
// once, and finalize the attempt/task once the Harness run has an answer.
// It is satisfied directly by *tasks.Service; ceochat never talks to
// PostgreSQL for task state itself.
type TaskCoordinator interface {
	CreateTask(ctx context.Context, request tasks.CreateRequest, actorType, actorID string) (tasks.Task, bool, error)
	GetTask(ctx context.Context, id int64) (tasks.TaskDetail, error)
	ClaimTaskByID(ctx context.Context, taskID int64, request tasks.ClaimRequest) (tasks.ClaimedTask, error)
	StartAttempt(ctx context.Context, command tasks.LeaseCommand) (tasks.Task, error)
	RecordAttemptResult(ctx context.Context, command tasks.RecordAttemptResultCommand) (tasks.Task, error)
	FinalizeTask(ctx context.Context, command tasks.FinalizeCommand) (tasks.Task, error)
	BlockTask(ctx context.Context, command tasks.BlockCommand) (tasks.Task, error)
}

// TaskReader is the read-only slice of the Task Engine the tasks.* chat
// capabilities use. It is satisfied directly by *tasks.Service -- the same
// canonical service TaskCoordinator already wraps -- so a read-only
// capability and the turn-driving coordinator can never observe two
// different notions of task state.
type TaskReader interface {
	ListTasks(ctx context.Context, filter tasks.TaskFilter) ([]tasks.Task, error)
	GetTask(ctx context.Context, id int64) (tasks.TaskDetail, error)
	// ListAttemptsPage returns a real, server-side-bounded page (SQL
	// LIMIT/OFFSET, not "fetch everything, then slice") -- see
	// tasks.Service.ListAttemptsPage.
	ListAttemptsPage(ctx context.Context, taskID int64, limit, offset int) ([]tasks.Attempt, error)
}

// RunDescriptorRecord is the read-only projection of one Harness run's
// immutable descriptor the runs.* tools need: exactly the fields
// RunDescriptorStore.ReadRunDescriptor already returns, plus CreatedAt for
// ordering a list by recency. It intentionally carries no trajectory
// (prompts, tool bodies, provider reasoning) -- a descriptor is an
// execution identity record, never an execution log.
type RunDescriptorRecord struct {
	executionharness.RunDescriptor
	CreatedAt time.Time
}

// RunDescriptorFilter bounds one RunLister.ListRunDescriptors call.
type RunDescriptorFilter struct {
	TaskID             int64
	ExecutionProfileID string
	Limit              int
	Offset             int
}

// RunLister is the read-only slice of the durable run-descriptor store the
// runs.list_recent capability needs. The production implementation adapts
// *executionharnesspostgres.Store.ListRunDescriptors -- the same canonical
// table EnsureRunDescriptor/ReadRunDescriptor already own, not a new store.
type RunLister interface {
	ListRunDescriptors(ctx context.Context, filter RunDescriptorFilter) ([]RunDescriptorRecord, error)
}

// RunEventReader is the read-only slice of ExecutionHistoryStore the
// runs.get capability needs to derive a run's outcome: its own durable
// trajectory, read the same way the Harness itself reads it to resume a
// run. It is satisfied directly by whatever Service.HarnessHistory already
// is -- runs.* never opens a second history store.
type RunEventReader interface {
	Read(ctx context.Context, runID string) ([]executionharness.Event, error)
}
