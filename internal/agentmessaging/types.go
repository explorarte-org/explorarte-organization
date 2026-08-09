package agentmessaging

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusClaimed   Status = "claimed"
	StatusDelivered Status = "delivered"
	StatusDead      Status = "dead"
)

type MessageType string

const (
	MessageDelegation MessageType = "delegation"
	MessageCompletion MessageType = "completion"
	MessageStatus     MessageType = "status"
)

func (k MessageType) Valid() bool {
	switch k {
	case MessageDelegation, MessageCompletion, MessageStatus:
		return true
	default:
		return false
	}
}

type Message struct {
	ID              int64
	OrganizationID  string
	SenderRoleID    string
	SenderTaskID    int64
	RecipientRoleID string
	RecipientTaskID *int64
	CorrelationID   string
	CausationID     string
	MessageType     MessageType
	Payload         json.RawMessage
	IdempotencyKey  string
	Status          Status
	AttemptCount    int
	MaxAttempts     int
	ClaimedBy       *string
	ClaimExpiresAt  *time.Time
	LastError       *string
	AvailableAt     time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeliveredAt     *time.Time
}

type SendCommand struct {
	OrganizationID  string
	SenderRoleID    string
	SenderTaskID    int64
	RecipientRoleID string
	RecipientTaskID *int64
	CorrelationID   string
	CausationID     string
	MessageType     MessageType
	Payload         json.RawMessage
	IdempotencyKey  string
	MaxAttempts     int
}

func (c SendCommand) Validate() error {
	if strings.TrimSpace(c.OrganizationID) == "" || strings.TrimSpace(c.SenderRoleID) == "" || strings.TrimSpace(c.RecipientRoleID) == "" {
		return fmt.Errorf("%w: organization, sender, and recipient roles are required", ErrInvalidRequest)
	}
	if c.SenderTaskID <= 0 {
		return fmt.Errorf("%w: sender task id must be positive", ErrInvalidRequest)
	}
	if c.RecipientTaskID != nil && *c.RecipientTaskID <= 0 {
		return fmt.Errorf("%w: recipient task id must be positive when set", ErrInvalidRequest)
	}
	if strings.TrimSpace(c.CorrelationID) == "" || strings.TrimSpace(c.CausationID) == "" {
		return fmt.Errorf("%w: correlation and causation ids are required", ErrInvalidRequest)
	}
	if !c.MessageType.Valid() {
		return fmt.Errorf("%w: invalid message type %q", ErrInvalidRequest, c.MessageType)
	}
	if len(c.Payload) == 0 || !json.Valid(c.Payload) {
		return fmt.Errorf("%w: payload must be valid, non-empty JSON", ErrInvalidRequest)
	}
	if strings.TrimSpace(c.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidRequest)
	}
	if c.MaxAttempts <= 0 || c.MaxAttempts > 100 {
		return fmt.Errorf("%w: max attempts must be between 1 and 100", ErrInvalidRequest)
	}
	return nil
}

type ClaimedMessage struct {
	Message    Message
	ClaimToken string
}

type Disposition struct {
	MessageID  int64
	ConsumerID string
	ClaimToken string
	Error      string
}
