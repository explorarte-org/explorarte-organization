// Package ceochat is the durable owner<->CEO conversational surface.
//
// It is a sibling of internal/executive, not a part of it: a chat turn is
// driven by the same Task Engine authority, Model Runtime, and Execution
// Harness the Executive uses, under its own execution profile
// (executive/chat/v1), but ceochat owns no typed-task semantics and the
// Executive owns no chat semantics. Neither package imports the other.
//
// ceochat persists exactly two things: the conversation and its messages.
// The cognitive/tool-call trajectory that produces an assistant message
// already has a durable home in internal/executionharness (run history +
// run descriptor); this package references that trajectory by run/task/
// attempt ID and never duplicates its content.
package ceochat

import "time"

// ConversationStatus is a conversation's lifecycle state. V1 never closes a
// conversation itself; the column exists so a later round can.
type ConversationStatus string

const (
	ConversationActive ConversationStatus = "active"
	ConversationClosed ConversationStatus = "closed"
)

type Conversation struct {
	ID             int64
	OrganizationID string
	// OwnerRoleID is the only actor role allowed to Send on this
	// conversation. It is fixed at creation; there is no per-message actor
	// override.
	OwnerRoleID string
	Status      ConversationStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MessageRole distinguishes the two sides of a conversation. There is no
// "system" or "tool" role here -- those belong to the Harness's own event
// ledger for the turn, not to the owner-visible conversation.
type MessageRole string

const (
	MessageOwner     MessageRole = "owner"
	MessageAssistant MessageRole = "assistant"
)

// Message is one owner or assistant entry in a conversation's durable,
// sequence-ordered log.
type Message struct {
	ID             int64
	ConversationID int64
	OrganizationID string
	// Sequence is assigned by the store, monotonically per conversation. It
	// is the ordering a caller replays History() in, not creation
	// wall-clock time.
	Sequence int64
	Role     MessageRole
	Content  string
	// IdempotencyKey is set only on an owner message: it is the anchor
	// Send's whole idempotency contract rests on. Always empty on an
	// assistant message.
	IdempotencyKey string
	// TaskID/AttemptID/RunID name the durable task attempt and Harness run
	// that produced (assistant) or was created to answer (owner) this
	// message. TaskID is always set for an owner message; AttemptID/RunID
	// are set once a claim/run actually happened.
	TaskID        int64
	AttemptID     int64
	RunID         string
	CorrelationID string
	CausationID   string
	CreatedAt     time.Time
}

// CreateConversationRequest opens a new conversation. ActorRoleID is who is
// asking (must equal OwnerRoleID: a conversation can only be opened by the
// owner it will belong to -- there is no "open on someone else's behalf" in
// V1).
type CreateConversationRequest struct {
	OrganizationID string
	ActorRoleID    string
	OwnerRoleID    string
}

// SendRequest is one owner message. IdempotencyKey is mandatory: it is what
// makes Send safe to retry after a crash or a network failure, and it is
// the sole key History-level duplicate detection compares against.
type SendRequest struct {
	OrganizationID string
	ConversationID int64
	ActorRoleID    string
	IdempotencyKey string
	Content        string
}

// RunOutcome classifies how the Harness run behind a Send resolved, for
// callers that want more than "did I get an assistant message".
type RunOutcome string

const (
	RunOutcomeCompleted   RunOutcome = "completed"
	RunOutcomeIncomplete  RunOutcome = "incomplete"
	RunOutcomeUnavailable RunOutcome = "authority_unavailable"
)

// SendResult reports what Send did. Reused is true when the SAME
// idempotency key on the SAME conversation was seen before: no new task,
// attempt, Harness run, model call, or tool execution happened, and
// OwnerMessage/AssistantMessage are the ones durably recorded the first
// time.
type SendResult struct {
	Reused           bool
	Outcome          RunOutcome
	OwnerMessage     Message
	AssistantMessage *Message
	TurnsUsed        int
	ToolCallsUsed    int
}

// HistoryRequest bounds a read of a conversation's message log.
type HistoryRequest struct {
	OrganizationID string
	ConversationID int64
	// ActorRoleID is the caller reading this history. It must equal the
	// conversation's OwnerRoleID -- exactly the same boundary Send already
	// enforces before recording an owner message -- or History returns
	// ErrUnauthorizedActor before any message is read. A conversation's
	// transcript is no less sensitive than the ability to add to it.
	ActorRoleID string
	// Limit bounds the number of most-recent messages returned. <=0 means
	// the service's own default bound (see DefaultHistoryMessageLimit).
	Limit int
}
