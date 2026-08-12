package agentmessaging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// computeCanonicalRequestHashForTest mirrors postgres/store.computeCanonicalRequestHash
// for use in unit tests where we cannot access unexported functions.
func computeCanonicalRequestHashForTest(cmd SendCommand) string {
	hashInput := map[string]interface{}{
		"schema_version":  cmd.SchemaVersion,
		"organization_id": cmd.OrganizationID,
		"sender_role_id":  cmd.SenderRoleID,
		"sender_task_id":  cmd.SenderTaskID,
		"max_attempts":    cmd.MaxAttempts,
	}
	if cmd.RecipientTaskID != nil {
		hashInput["recipient_task_id"] = *cmd.RecipientTaskID
	} else {
		hashInput["recipient_task_id"] = nil
	}
	hashInput["recipient_role_id"] = cmd.RecipientRoleID
	hashInput["correlation_id"] = cmd.CorrelationID
	hashInput["causation_id"] = cmd.CausationID
	hashInput["message_type"] = string(cmd.MessageType)
	hashInput["payload_canonical"] = string(cmd.Payload)
	hashInput["idempotency_key"] = cmd.IdempotencyKey

	canonicalBytes, _ := json.Marshal(hashInput)
	digest := sha256.Sum256(canonicalBytes)
	return hex.EncodeToString(digest[:])
}
func TestSendCommandValidateRejectsPayloadExceedingMaxBytes(t *testing.T) {
	// The payload-size check runs AFTER the json.Valid() check in
	// Validate(), so the fixture must be valid JSON, not just oversized
	// raw bytes, or it would fail the JSON-validity check first and never
	// actually exercise ErrPayloadTooLarge.
	oversizedPayload := []byte(`{"delegated_task_id":1,"padding":"` + strings.Repeat("x", MaxPayloadBytes) + `"}`)
	cmd := SendCommand{
		OrganizationID:  "test",
		SenderRoleID:    "test/role",
		SenderTaskID:    1,
		RecipientRoleID: "test/leader",
		CorrelationID:   "corr",
		CausationID:     "caus",
		MessageType:     MessageDelegation,
		Payload:         oversizedPayload,
		SchemaVersion:   SchemaVersionV1,
		IdempotencyKey:  "test-key",
		MaxAttempts:     5,
	}
	if err := cmd.Validate(); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestSendCommandValidateSemanticInvariant_Delegation(t *testing.T) {
	delegatedTaskID := int64(123)
	payload := json.RawMessage(`{"delegated_task_id":123}`)
	cmd := SendCommand{
		OrganizationID:  "org",
		SenderRoleID:    "ceo",
		RecipientRoleID: "worker",
		SenderTaskID:    1,
		RecipientTaskID: &delegatedTaskID,
		CorrelationID:   "c",
		CausationID:     "cau",
		MessageType:     MessageDelegation,
		Payload:         payload,
		IdempotencyKey:  "key",
		MaxAttempts:     5,
		SchemaVersion:   SchemaVersionV1,
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("valid delegation should pass: %v", err)
	}

	// Mismatched IDs should fail
	wrongRecipTaskID := int64(456)
	cmd.RecipientTaskID = &wrongRecipTaskID
	if err := cmd.Validate(); !strings.Contains(err.Error(), "invariant") {
		t.Fatalf("expected invariant violation, got %v", err)
	}
}

func TestSendCommandValidateSemanticInvariant_Completion(t *testing.T) {
	senderTaskID := int64(789)
	payload := json.RawMessage(`{"completed_task_id":789}`)
	cmd := SendCommand{
		OrganizationID:  "org",
		SenderRoleID:    "worker",
		RecipientRoleID: "leader",
		SenderTaskID:    senderTaskID, // Must match CompletedTaskID
		RecipientTaskID: ptr(int64(1)),
		CorrelationID:   "c",
		CausationID:     "cau",
		MessageType:     MessageCompletion,
		Payload:         payload,
		IdempotencyKey:  "key",
		MaxAttempts:     5,
		SchemaVersion:   SchemaVersionV1,
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("valid completion should pass: %v", err)
	}

	// Mismatched SenderTaskID vs CompletedTaskID should fail
	cmd.Payload = json.RawMessage(`{"completed_task_id":999}`)
	if err := cmd.Validate(); !strings.Contains(err.Error(), "invariant") {
		t.Fatalf("expected invariant violation for completion, got %v", err)
	}
}

func TestCanonicalRequestHashDeterministic(t *testing.T) {
	delegatedTaskID := int64(123)
	cmd := SendCommand{
		OrganizationID:  "org1",
		SenderRoleID:    "ceo",
		SenderTaskID:    1,
		RecipientRoleID: "worker",
		RecipientTaskID: &delegatedTaskID,
		CorrelationID:   "corr",
		CausationID:     "caus",
		MessageType:     MessageDelegation,
		Payload:         json.RawMessage(`{"delegated_task_id":123}`),
		IdempotencyKey:  "key",
		MaxAttempts:     5,
		SchemaVersion:   SchemaVersionV1,
	}

	hash1 := computeCanonicalRequestHashForTest(cmd)
	hash2 := computeCanonicalRequestHashForTest(cmd)

	if hash1 != hash2 {
		t.Fatal("same command produced different hashes")
	}

	// Different max_attempts should produce different hash
	cmd.MaxAttempts = 10
	hash3 := computeCanonicalRequestHashForTest(cmd)
	if hash1 == hash3 {
		t.Fatal("different max_attempts produced same hash")
	}
}

func TestSendCommandValidateRejectsUnknownFieldsDelegation(t *testing.T) {
	payload := json.RawMessage(`{"delegated_task_id":123,"unknown_field":true}`)
	delegatedTaskID := int64(123)
	cmd := SendCommand{
		OrganizationID:  "org",
		SenderRoleID:    "ceo",
		RecipientRoleID: "worker",
		SenderTaskID:    1,
		RecipientTaskID: &delegatedTaskID,
		CorrelationID:   "c",
		CausationID:     "cau",
		MessageType:     MessageDelegation,
		Payload:         payload,
		IdempotencyKey:  "key",
		MaxAttempts:     5,
		SchemaVersion:   SchemaVersionV1,
	}
	if err := cmd.Validate(); !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field rejection, got %v", err)
	}
}

func TestSendCommandRejectsDuplicateJSONKeys(t *testing.T) {
	// encoding/json does NOT reject duplicate keys on its own -- for both
	// map[string]interface{} and a typed struct, Unmarshal silently keeps
	// the LAST occurrence of a duplicate key. This test exercises the real
	// enforcement point (SendCommand.Validate, via rejectDuplicateJSONKeys)
	// rather than a standalone json.Unmarshal call, so it fails if that
	// wiring ever regresses.
	delegatedTaskID := int64(1)
	base := SendCommand{
		OrganizationID:  "org",
		SenderRoleID:    "ceo",
		SenderTaskID:    1,
		RecipientRoleID: "worker",
		RecipientTaskID: &delegatedTaskID,
		CorrelationID:   "c",
		CausationID:     "cau",
		MessageType:     MessageDelegation,
		IdempotencyKey:  "key",
		MaxAttempts:     5,
		SchemaVersion:   SchemaVersionV1,
	}

	topLevel := base
	topLevel.Payload = json.RawMessage(`{"delegated_task_id":1,"delegated_task_id":2}`)
	if err := topLevel.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("expected duplicate JSON key rejection for top-level duplicate, got %v", err)
	}

	// Duplicate key nested inside an object value must be rejected too,
	// not just a top-level duplicate -- rejectDuplicateJSONKeys tracks a
	// separate key set per open object at every nesting depth.
	nested := base
	nested.Payload = json.RawMessage(`{"delegated_task_id":1,"extra":{"x":1,"x":2}}`)
	if err := nested.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("expected duplicate JSON key rejection for nested duplicate, got %v", err)
	}

	// Sanity: the same key appearing once in two DIFFERENT objects (not a
	// real duplicate) must not be flagged.
	notDuplicate := base
	notDuplicate.Payload = json.RawMessage(`{"delegated_task_id":1,"extra":{"x":1},"x":2}`)
	if err := notDuplicate.Validate(); err != nil && strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("same key in two different objects must not be flagged as duplicate, got %v", err)
	}

	// Sanity: a genuinely valid, duplicate-free delegation payload must
	// still pass.
	valid := base
	valid.Payload = json.RawMessage(`{"delegated_task_id":1}`)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid duplicate-free payload should pass Validate: %v", err)
	}
}

func TestRejectDuplicateJSONKeysDirectly(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{"top-level duplicate", `{"a":1,"a":2}`, true},
		{"nested duplicate", `{"a":{"x":1,"x":2}}`, true},
		{"duplicate inside array element", `{"items":[{"a":1,"a":2}]}`, true},
		{"same key across sibling objects is fine", `{"a":{"x":1},"b":{"x":1}}`, false},
		{"same key across array elements is fine", `{"items":[{"a":1},{"a":1}]}`, false},
		{"no duplicates, deeply nested", `{"a":{"b":{"c":1}},"d":2}`, false},
		{"malformed JSON", `{"a":1,`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectDuplicateJSONKeys([]byte(tc.payload))
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error for %q, got nil", tc.payload)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got %v", tc.payload, err)
			}
		})
	}
}

// Helper functions
func ptr[T any](v T) *T {
	return &v
}
