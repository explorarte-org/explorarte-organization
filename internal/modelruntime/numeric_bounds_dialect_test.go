package modelruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSchemaDialectAcceptsNumericBounds(t *testing.T) {
	now := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	base := CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 1, AttemptID: 1,
		SubjectRoleID: "a/y", ContextSnapshotID: 1, Purpose: "test",
		OutputMode: OutputJSON, MaxOutputTokens: 10, ThinkingMode: ThinkingDisabled,
		IdempotencyKey: "x", Deadline: now.Add(time.Minute),
	}
	accept := []string{
		`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`,
		`{"type":"object","properties":{"val":{"type":"number","minimum":0.5,"maximum":99.5}},"additionalProperties":false}`,
		`{"type":"object","properties":{"unbounded_max":{"type":"integer","minimum":1}},"additionalProperties":false}`,
		`{"type":"object","properties":{"unbounded_min":{"type":"integer","maximum":100}},"additionalProperties":false}`,
		`{"type":"object","properties":{"zero_bounds":{"type":"integer","minimum":0,"maximum":0}},"additionalProperties":false}`,
	}
	for _, schema := range accept {
		cmd := base
		cmd.OutputSchema = []byte(schema)
		if _, _, _, err := PrepareCreateCommand(cmd, now); err != nil {
			t.Fatalf("schema with numeric bounds must be accepted: %v (schema: %s)", err, schema)
		}
	}
}

func TestSchemaDialectRejectsInvalidNumericBounds(t *testing.T) {
	now := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	base := CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 1, AttemptID: 1,
		SubjectRoleID: "a/y", ContextSnapshotID: 1, Purpose: "test",
		OutputMode: OutputJSON, MaxOutputTokens: 10, ThinkingMode: ThinkingDisabled,
		IdempotencyKey: "x", Deadline: now.Add(time.Minute),
	}
	reject := map[string]string{
		"min_greater_than_max":        `{"type":"object","properties":{"n":{"type":"integer","minimum":50,"maximum":20}}}`,
		"fractional_min_for_integer":  `{"type":"object","properties":{"n":{"type":"integer","minimum":1.5}}}`,
		"fractional_max_for_integer":  `{"type":"object","properties":{"n":{"type":"integer","maximum":10.5}}}`,
		"string_type_with_minimum":    `{"type":"object","properties":{"s":{"type":"string","minimum":1}}}`,
		"string_type_with_maximum":    `{"type":"object","properties":{"s":{"type":"string","maximum":10}}}`,
		"object_type_with_minimum":    `{"type":"object","properties":{"o":{"type":"object","minimum":1}}}`,
		"non_numeric_minimum_string":  `{"type":"object","properties":{"n":{"type":"integer","minimum":"1"}}}`,
		"non_numeric_maximum_boolean": `{"type":"object","properties":{"n":{"type":"integer","maximum":true}}}`,
	}
	for name, schema := range reject {
		cmd := base
		cmd.OutputSchema = []byte(schema)
		if _, _, _, err := PrepareCreateCommand(cmd, now); err == nil {
			t.Fatalf("%s: invalid numeric bound schema must be rejected: %s", name, schema)
		}
	}
}

func TestToolDefinitionsAcceptNumericBounds(t *testing.T) {
	rendered := []byte("authorized canonical context")
	envelope := ModelInputEnvelope{
		SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: 7,
		CanonicalProjectionDigest: SHA256Bytes([]byte("harness projection")),
		StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(rendered)}},
		ToolDefinitions: []ModelInputToolDefinition{
			{
				Name:        "tasks_list",
				Description: "List tasks with bounded limit",
				InputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"limit": {
							"type": "integer",
							"minimum": 1,
							"maximum": 50,
							"description": "Maximum rows"
						}
					}
				}`),
			},
		},
	}
	prepared, err := PrepareModelInput(&envelope, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatalf("PrepareModelInput must accept tools with minimum and maximum: %v", err)
	}
	if len(prepared.Envelope.ToolDefinitions) != 1 {
		t.Fatalf("expected 1 normalized tool, got %d", len(prepared.Envelope.ToolDefinitions))
	}
}

func TestValidateAgainstSchemaEnforcesNumericBounds(t *testing.T) {
	schemaJSON := []byte(`{
		"type": "object",
		"properties": {
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 50
			}
		}
	}`)
	var schema map[string]any
	dec := json.NewDecoder(strings.NewReader(string(schemaJSON)))
	dec.UseNumber()
	if err := dec.Decode(&schema); err != nil {
		t.Fatal(err)
	}

	// Valid values: within bounds and at edges
	for _, val := range []string{"1", "20", "50"} {
		obj := map[string]any{"limit": json.Number(val)}
		if err := validateAgainstSchema(obj, schema, "$"); err != nil {
			t.Errorf("val=%s should pass validation: %v", val, err)
		}
	}

	// Value below minimum (0 < 1)
	objUnder := map[string]any{"limit": json.Number("0")}
	if err := validateAgainstSchema(objUnder, schema, "$"); err == nil {
		t.Error("limit=0 must fail validation (minimum 1)")
	}

	// Value above maximum (51 > 50)
	objOver := map[string]any{"limit": json.Number("51")}
	if err := validateAgainstSchema(objOver, schema, "$"); err == nil {
		t.Error("limit=51 must fail validation (maximum 50)")
	}
}

func TestCanonicalizeRawJSONPreservesNumericBounds(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 50
			}
		}
	}`)
	canonical, err := CanonicalizeRawJSON(raw)
	if err != nil {
		t.Fatalf("CanonicalizeRawJSON: %v", err)
	}
	s := string(canonical)
	if !strings.Contains(s, `"minimum":1`) || !strings.Contains(s, `"maximum":50`) {
		t.Fatalf("canonical JSON must preserve minimum and maximum: %s", s)
	}
}
