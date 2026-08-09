package agentmessaging

import (
	"context"
	"time"
)

// Ledger is the transactional boundary for the agent message inbox. Send
// is idempotent per (organizationID, idempotencyKey) — a retried
// delegation or completion call for the same task never double-sends.
type Ledger interface {
	Send(ctx context.Context, command SendCommand, now time.Time) (message Message, reused bool, err error)
	ClaimNext(ctx context.Context, organizationID, recipientRoleID, consumerID string, batchSize int, claimDuration time.Duration, now time.Time) ([]ClaimedMessage, error)
	Ack(ctx context.Context, disposition Disposition, now time.Time) error
	Nack(ctx context.Context, disposition Disposition, now time.Time) error
}
