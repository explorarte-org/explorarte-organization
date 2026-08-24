package modelruntime

import (
	"strings"
	"testing"
	"time"
)

// R7 fix -- CONTRACT COMMUNICATION support in the schema dialect.
//
// Executive's worker-result/v2 schema now declares maxLength on its
// byte-limited fields so the model is told the limit before it attempts.
// These guards pin both halves of the decision at the Model Runtime
// boundary:
//
//  1. maxLength is an accepted keyword of the stored/rendered schema
//     dialect (positive integer), so the contract reaches the prompt.
//  2. maxLength is deliberately NOT enforced by response normalization:
//     JSON-Schema maxLength counts code points while the host rule is
//     UTF-8 bytes measured by executive.validateRequiredString, and
//     rejecting here would reclassify a host-side contract rejection
//     ("model_result_contract_rejected", retryable with feedback) as a
//     provider normalization failure.
func TestSchemaDialectAcceptsMaxLength(t *testing.T) {
	now := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	base := CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 1, AttemptID: 1,
		SubjectRoleID: "a/y", ContextSnapshotID: 1, Purpose: "test",
		OutputMode: OutputJSON, MaxOutputTokens: 10, ThinkingMode: ThinkingDisabled,
		IdempotencyKey: "x", Deadline: now.Add(time.Minute),
	}
	accept := []string{
		`{"type":"object","properties":{"s":{"type":"string","maxLength":4000}},"required":["s"],"additionalProperties":false}`,
	}
	for _, schema := range accept {
		cmd := base
		cmd.OutputSchema = []byte(schema)
		if _, _, _, err := PrepareCreateCommand(cmd, now); err != nil {
			t.Fatalf("maxLength must be part of the accepted schema dialect: %v", err)
		}
	}
	reject := map[string]string{
		"zero":     `{"type":"object","properties":{"s":{"type":"string","maxLength":0}}}`,
		"negative": `{"type":"object","properties":{"s":{"type":"string","maxLength":-1}}}`,
		"fraction": `{"type":"object","properties":{"s":{"type":"string","maxLength":4.5}}}`,
		"string":   `{"type":"object","properties":{"s":{"type":"string","maxLength":"4000"}}}`,
	}
	for name, schema := range reject {
		cmd := base
		cmd.OutputSchema = []byte(schema)
		if _, _, _, err := PrepareCreateCommand(cmd, now); err == nil {
			t.Fatalf("%s: invalid maxLength must be rejected", name)
		} else if !strings.Contains(err.Error(), "maxLength") {
			t.Fatalf("%s: rejection must name maxLength, got: %v", name, err)
		}
	}
}

func TestNormalizerDoesNotEnforceMaxLengthOnResponses(t *testing.T) {
	n := Normalizer{MaxResponseBytes: 4096}
	inv := Invocation{ID: 1, OutputMode: OutputJSON, OutputSchema: []byte(`{"type":"object","properties":{"s":{"type":"string","maxLength":4}},"required":["s"],"additionalProperties":false}`)}
	if _, err := n.Normalize(inv, 1, RawResponse{Content: []byte(`{"s":"this string is far longer than four characters"}`)}); err != nil {
		t.Fatalf("maxLength is communication, not boundary enforcement: %v", err)
	}
}
