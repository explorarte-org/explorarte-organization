package agentmessaging

import "errors"

var (
	ErrInvalidRequest     = errors.New("invalid agent message request")
	ErrNotFound           = errors.New("agent message not found")
	ErrConflict           = errors.New("agent message claim conflict")
	ErrClaimExpired       = errors.New("agent message claim expired")
	ErrRateLimited        = errors.New("agent message rate limit exceeded")
	ErrPayloadTooLarge    = errors.New("agent message payload exceeds maximum size")
	ErrSchemaMismatch     = errors.New("payload schema version mismatch")
	ErrInvariantViolation = errors.New("message semantic invariant violated")
)
