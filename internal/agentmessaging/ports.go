package agentmessaging

import (
	"context"
	"time"
)

// Capabilities for agent messaging authorization.
const (
	CapabilityAgentMessageSend   = "agent.message.send"
	CapabilityAgentMessageClaim  = "agent.message.claim"
	CapabilityAgentMessageSettle = "agent.message.settle"
)

// Ledger is the transactional boundary for the agent message inbox. Send
// is idempotent per (organizationID, idempotencyKey) — a retried
// delegation or completion call for the same task never double-sends.
//
// CRITICAL: ALL operations require an authenticated execution principal.
// There is NO fallback to free-string consumerIDs in production.
type Ledger interface {
	// Send requires executionPrincipalID bound to sender_role_id.
	// Validates: principal.dispatch_actor_role_id == command.sender_role_id
	// AND task ownership checks, topology enforcement, capability evaluation.
	Send(ctx context.Context, executionPrincipalID string, command SendCommand, now time.Time) (Message, bool, error)

	// ClaimNext requires executionPrincipalID that matches recipientRoleID.
	// Validates: principal exists + active + dispatch_actor_role_id == recipientRoleID.
	// Returns messages whose inbox matches this principal's role scope.
	ClaimNext(ctx context.Context, executionPrincipalID, organizationID, recipientRoleID string, batchSize int, claimDuration time.Duration, now time.Time) ([]ClaimedMessage, error)

	// Ack/Nack retain token verification but also verify executionPrincipalID.
	// The claimed_by field must match the principal performing settlement.
	Ack(ctx context.Context, executionPrincipalID string, disposition Disposition, now time.Time) error
	Nack(ctx context.Context, executionPrincipalID string, disposition Disposition, now time.Time) error
}
