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
)
