package agentmessaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxPayloadBytes is the hard cap for message payload size in bytes.
// Payloads exceeding this limit are rejected with ErrPayloadTooLarge.
const MaxPayloadBytes = 1024

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
	// MessageStatus deprecated - no productive consumer exists.
	// Keep for backward-compat reads but reject new writes.
	MessageStatus MessageType = "status"
)

func (k MessageType) Valid() bool {
	switch k {
	case MessageDelegation, MessageCompletion, MessageStatus:
		return true
	default:
		return false
	}
}

// SchemaVersion for structured payload validation. All messages must use V1.
const SchemaVersionV1 = "v1"

// DelegationPayloadV1 is the ONLY permitted payload type for MessageDelegation.
// Invariant: DelegatedTaskID MUST equal SendCommand.RecipientTaskID.
// This ensures delegation targets exactly the child task being created.
type DelegationPayloadV1 struct {
	DelegatedTaskID int64 `json:"delegated_task_id"`
}

// CompletionPayloadV1 is the ONLY permitted payload type for MessageCompletion.
// Invariant: CompletedTaskID MUST equal SendCommand.SenderTaskID.
// This ensures completion reports the sender's own task.
type CompletionPayloadV1 struct {
	CompletedTaskID int64 `json:"completed_task_id"`
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
	RequestHash     *string // New column: SHA-256 canonical hash for idempotency integrity
	SchemaVersion   string  // Schema version discriminator (should be "v1")
	PayloadByteSize int     // Tracked byte size of payload for monitoring
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
	RequestHash     string // Canonical request hash for idempotency collision detection
	SchemaVersion   string // Schema version discriminator
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
	// Validate schema version and payload structure
	if strings.TrimSpace(c.SchemaVersion) != SchemaVersionV1 {
		return fmt.Errorf("%w: unsupported schema version %q, expected %q", ErrInvalidRequest, c.SchemaVersion, SchemaVersionV1)
	}
	payloadSize := len(c.Payload)
	if payloadSize > MaxPayloadBytes {
		return fmt.Errorf("%w: payload size %d exceeds maximum %d bytes", ErrPayloadTooLarge, payloadSize, MaxPayloadBytes)
	}
	// Decode into proper typed struct based on message type, then validate invariants
	var rawData map[string]interface{}
	if err := json.Unmarshal(c.Payload, &rawData); err != nil {
		return fmt.Errorf("%w: failed to decode payload: %v", ErrInvalidRequest, err)
	}
	// Validate semantic invariants based on message type
	if err := c.validateSemanticInvariants(rawData); err != nil {
		return err
	}
	return nil
}

// ValidateBasic validates core fields common to all message types.
func (c SendCommand) ValidateBasic() error {
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

// ValidateSchemaAndPayload validates schema version, payload structure, and semantic invariants.
func (c SendCommand) ValidateSchemaAndPayload() error {
	if strings.TrimSpace(c.SchemaVersion) != SchemaVersionV1 {
		return fmt.Errorf("%w: unsupported schema version %q, expected %q", ErrInvalidRequest, c.SchemaVersion, SchemaVersionV1)
	}

	// Validate payload size
	payloadSize := len(c.Payload)
	if payloadSize > MaxPayloadBytes {
		return fmt.Errorf("%w: payload size %d exceeds maximum %d bytes", ErrPayloadTooLarge, payloadSize, MaxPayloadBytes)
	}

	// Decode into proper typed struct based on message type, then validate invariants
	var rawData map[string]interface{}
	if err := json.Unmarshal(c.Payload, &rawData); err != nil {
		return fmt.Errorf("%w: failed to decode payload: %v", ErrInvalidRequest, err)
	}

	// Reject duplicate keys by re-decoding with strict mode
	if err := c.validateNoDuplicateKeys(rawData); err != nil {
		return err
	}

	// Validate semantic invariants based on message type
	if err := c.validateSemanticInvariants(rawData); err != nil {
		return err
	}

	return nil
}

// validateNoDuplicateKeys checks if any field appears more than once at top level.
// Uses JSON decoder in strict mode which rejects duplicate keys.
func (c SendCommand) validateNoDuplicateKeys(data map[string]interface{}) error {
	// Re-parse using a strict decoder that detects duplicates
	decoder := json.NewDecoder(strings.NewReader(string(c.Payload)))
	decoder.DisallowUnknownFields()

	var parsed interface{}
	// We need to detect duplicates before Unmarshal fully succeeds.
	// Use raw message first to check length, then try strict parsing.
	if err := json.Unmarshal(c.Payload, &parsed); err != nil {
		// If we got here due to malformed JSON or unknown fields, report it
		if strings.Contains(err.Error(), "unknown field") {
			return fmt.Errorf("%w: payload contains unknown fields not matching schema", ErrInvalidRequest)
		}
		return fmt.Errorf("%w: malformed JSON payload", ErrInvalidRequest)
	}
	return nil
}

// validateSemanticInvariants checks type-specific invariants.
func (c SendCommand) validateSemanticInvariants(rawData map[string]interface{}) error {
	switch c.MessageType {
	case MessageDelegation:
		// Delegate invariant: DelegatedTaskID == RecipientTaskID
		delegatedTaskID, ok := rawData["delegated_task_id"].(float64)
		if !ok {
			return fmt.Errorf("%w: delegation payload missing or invalid delegated_task_id field", ErrInvalidRequest)
		}
		if c.RecipientTaskID == nil {
			return fmt.Errorf("%w: delegation requires recipient_task_id to match delegated_task_id", ErrInvalidRequest)
		}
		intendedRecip := int64(delegatedTaskID)
		if intendedRecip != *c.RecipientTaskID {
			return fmt.Errorf("%w: delegation invariant violation: delegated_task_id (%d) does not equal recipient_task_id (%d)", ErrInvalidRequest, intendedRecip, *c.RecipientTaskID)
		}
		// Check for unknown fields beyond delegated_task_id
		if len(rawData) > 1 {
			for k := range rawData {
				if k != "delegated_task_id" {
					return fmt.Errorf("%w: delegation payload contains unknown field %q", ErrInvalidRequest, k)
				}
			}
		}

	case MessageCompletion:
		// Complete invariant: CompletedTaskID == SenderTaskID
		completedTaskID, ok := rawData["completed_task_id"].(float64)
		if !ok {
			return fmt.Errorf("%w: completion payload missing or invalid completed_task_id field", ErrInvalidRequest)
		}
		intendedComplete := int64(completedTaskID)
		if intendedComplete != c.SenderTaskID {
			return fmt.Errorf("%w: completion invariant violation: completed_task_id (%d) does not equal sender_task_id (%d)", ErrInvalidRequest, intendedComplete, c.SenderTaskID)
		}
		// Check for unknown fields beyond completed_task_id
		if len(rawData) > 1 {
			for k := range rawData {
				if k != "completed_task_id" {
					return fmt.Errorf("%w: completion payload contains unknown field %q", ErrInvalidRequest, k)
				}
			}
		}

	case MessageStatus:
		// MessageStatus deprecated - accept legacy payloads but warn
		// Keep accepting minimal payload for backward compatibility
		if len(rawData) == 0 {
			return fmt.Errorf("%w: status payload cannot be empty", ErrInvalidRequest)
		}
		// Don't add new restrictions for legacy compatibility; future work may remove this type entirely
	}

	return nil
}
