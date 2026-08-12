package agentmessaging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	// NOTE: MessageStatus was removed in security hardening v1.
	// It posed a covert channel risk (arbitrary data in opaque JSONB) with no
	// productive consumer identified. All agent messaging must use one of the
	// two structured payload types with semantic invariant validation.
)

func (k MessageType) Valid() bool {
	switch k {
	case MessageDelegation, MessageCompletion:
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
	// Fail closed on duplicate JSON keys BEFORE any Unmarshal into a
	// map/struct -- encoding/json silently keeps the LAST occurrence of a
	// duplicate key for both map[string]interface{} and typed structs, it
	// never rejects them. A duplicate key is exactly the kind of
	// parser-differential a hostile payload could exploit (e.g. a review
	// step that only inspects the first occurrence disagreeing with a
	// consumer that applies the last one), so it must never reach the
	// semantic-invariant checks below at all.
	if err := rejectDuplicateJSONKeys(c.Payload); err != nil {
		return err
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

// jsonKeyScanFrame tracks duplicate-key detection state for one currently
// open JSON object or array while scanning a token stream. Only isObject
// frames track a `seen` set and alternate expectKey; array frames exist
// purely so the scanner knows values inside `[...]` are never object keys,
// even when those values are themselves objects.
type jsonKeyScanFrame struct {
	isObject  bool
	expectKey bool
	seen      map[string]struct{}
}

// rejectDuplicateJSONKeys fails closed the instant the same key appears
// twice within the same JSON object, at any nesting depth (including
// objects nested inside arrays). encoding/json's Unmarshal does NOT do
// this on its own for either map[string]interface{} or a typed struct --
// a duplicate key is silently resolved to its last occurrence. This is a
// pure json.Decoder.Token() stream scan: no external dependency, and it
// never builds the ambiguous value itself, only the key set per open
// object.
func rejectDuplicateJSONKeys(payload []byte) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var stack []*jsonKeyScanFrame
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: malformed JSON payload: %v", ErrInvalidRequest, err)
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				stack = append(stack, &jsonKeyScanFrame{isObject: true, expectKey: true, seen: map[string]struct{}{}})
			case '[':
				stack = append(stack, &jsonKeyScanFrame{isObject: false})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("%w: malformed JSON payload: unbalanced %v", ErrInvalidRequest, delim)
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].isObject {
					// The object/array that just closed was the value for
					// the key most recently seen in the parent object --
					// the parent now expects its next key.
					stack[len(stack)-1].expectKey = true
				}
			}
			continue
		}
		if len(stack) == 0 || !stack[len(stack)-1].isObject {
			continue
		}
		frame := stack[len(stack)-1]
		if frame.expectKey {
			key, ok := tok.(string)
			if !ok {
				return fmt.Errorf("%w: malformed JSON payload: expected object key", ErrInvalidRequest)
			}
			if _, duplicate := frame.seen[key]; duplicate {
				return fmt.Errorf("%w: duplicate JSON key %q", ErrInvalidRequest, key)
			}
			frame.seen[key] = struct{}{}
			frame.expectKey = false
		} else {
			// This token was a scalar value (string/number/bool/null) for
			// the key just recorded above -- the next token in this
			// object is a key again.
			frame.expectKey = true
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("%w: malformed JSON payload: unbalanced object or array", ErrInvalidRequest)
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
			return fmt.Errorf("%w: delegation invariant violation: delegated_task_id (%d) does not equal recipient_task_id (%d)", ErrInvariantViolation, intendedRecip, *c.RecipientTaskID)
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
			return fmt.Errorf("%w: completion invariant violation: completed_task_id (%d) does not equal sender_task_id (%d)", ErrInvariantViolation, intendedComplete, c.SenderTaskID)
		}
		// Check for unknown fields beyond completed_task_id
		if len(rawData) > 1 {
			for k := range rawData {
				if k != "completed_task_id" {
					return fmt.Errorf("%w: completion payload contains unknown field %q", ErrInvalidRequest, k)
				}
			}
		}
	}

	return nil
}

// ClaimedMessage wraps a Message with its cryptographic claim token for settlement.
type ClaimedMessage struct {
	Message    Message
	ClaimToken string // Plaintext token for Ack/Nack verification
}

// Disposition identifies a message for acknowledgment or negative acknowledgment.
// CRITICAL: ConsumerID MUST be the execution principal ID, NOT an arbitrary string.
type Disposition struct {
	MessageID  int64
	ConsumerID string // MUST match the execution principal that performed the ClaimNext (not arbitrary)
	ClaimToken string // Cryptographic proof of ownership (SHA-256 hashed in DB)
	Error      string // Optional error description for Nack
}
