package ceochat

import "errors"

var (
	// ErrInvalidInput marks a malformed request the caller must fix; retrying
	// unchanged never helps.
	ErrInvalidInput = errors.New("ceochat invalid input")
	// ErrConversationNotFound means the conversation ID does not exist for
	// this organization.
	ErrConversationNotFound = errors.New("ceochat conversation not found")
	// ErrUnauthorizedActor means the caller's ActorRoleID is not the
	// conversation's OwnerRoleID. No task, model, or tool side effect may
	// occur once this is returned.
	ErrUnauthorizedActor = errors.New("ceochat actor is not authorized for this conversation")
	// ErrIdempotencyConflict means the same (conversation, idempotency_key)
	// was already used for a DIFFERENT owner message. The caller must pick a
	// new key; the original message is never silently overwritten.
	ErrIdempotencyConflict = errors.New("ceochat idempotency key reused with different content")
	// ErrRunNotReady means the turn's task attempt is not currently in a
	// state Send can drive to completion (e.g. leased by a process that has
	// not proven it holds the lease token). The caller may retry later; no
	// side effect occurred.
	ErrRunNotReady = errors.New("ceochat turn is not ready to resume")
	// ErrDuplicateToolRegistration means a ToolRegistry.Register call named
	// an ID already registered. This is a bootstrap/test bug, never a
	// runtime condition: the model can neither trigger nor observe it.
	ErrDuplicateToolRegistration = errors.New("ceochat tool registry: duplicate tool registration")
	// ErrToolResultTooLarge means a capability's canonical-service call
	// returned more data than its descriptor's MaxResultBytes allows. The
	// registry refuses to hand it to the Harness; the caller (the model, on
	// its next turn) must narrow the request instead.
	ErrToolResultTooLarge = errors.New("ceochat tool result exceeds its bounded size limit")
)
