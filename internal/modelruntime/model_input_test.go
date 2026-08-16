package modelruntime

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

func modelInputSnapshot(rendered []byte) ContextSnapshotRef {
	return ContextSnapshotRef{ID: 7, RenderedHash: SHA256Bytes(rendered), DataClasses: []string{"organizational"}}
}

func TestPrepareModelInputIsCanonicalImmutableAndPrefixStable(t *testing.T) {
	rendered := []byte("authorized canonical context")
	input := ModelInputEnvelope{
		SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: 7,
		CanonicalProjectionDigest: SHA256Bytes([]byte("harness projection")),
		StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(rendered)}},
		VisibleHistory: []ModelInputMessage{
			{Role: ModelInputRoleAssistant, ToolCalls: []ModelInputToolCall{{ID: "call-1", Name: "lookup_fixture", Arguments: json.RawMessage(`{"z":1,"a":2}`)}}},
			{Role: ModelInputRoleTool, ToolCallID: "call-1", ToolName: "lookup_fixture", Content: `{"value":"safe"}`},
		},
		ToolDefinitions: []ModelInputToolDefinition{
			{Name: "z_tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "lookup_fixture", Description: "Read a fixture", InputSchema: json.RawMessage(`{"properties":{"id":{"type":"string"}},"type":"object"}`)},
		},
	}
	first, err := PrepareModelInput(&input, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareModelInput(&input, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CanonicalBytes, second.CanonicalBytes) || first.CanonicalDigest != second.CanonicalDigest {
		t.Fatal("same model input did not produce byte-identical canonical evidence")
	}
	if first.Envelope.ToolDefinitions[0].Name != "lookup_fixture" {
		t.Fatalf("tools were not normalized deterministically: %+v", first.Envelope.ToolDefinitions)
	}
	longer := cloneModelInputEnvelope(input)
	longer.VisibleHistory = append(longer.VisibleHistory, ModelInputMessage{Role: ModelInputRoleAssistant, Content: "next answer"})
	third, err := PrepareModelInput(&longer, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if third.Envelope.StablePrefixDigest != first.Envelope.StablePrefixDigest {
		t.Fatal("append-only visible history changed stable prefix identity")
	}
	toolDrift := cloneModelInputEnvelope(input)
	toolDrift.ToolDefinitions[0].Description = "changed action space"
	fourth, err := PrepareModelInput(&toolDrift, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Envelope.StablePrefixDigest == first.Envelope.StablePrefixDigest {
		t.Fatal("tool-definition drift did not change stable prefix identity")
	}
	if got := string(first.Envelope.VisibleHistory[0].ToolCalls[0].Arguments); got != `{"a":2,"z":1}` {
		t.Fatalf("tool arguments not canonical: %s", got)
	}
	stored, err := ValidateStoredModelInput(first, modelInputSnapshot(rendered))
	if err != nil || stored.CanonicalDigest != first.CanonicalDigest {
		t.Fatalf("stored input validation failed: %v", err)
	}
	mutated := first
	mutated.CanonicalBytes = append([]byte(nil), first.CanonicalBytes...)
	mutated.CanonicalBytes[len(mutated.CanonicalBytes)-1] ^= 1
	if _, err = ValidateStoredModelInput(mutated, modelInputSnapshot(rendered)); err == nil {
		t.Fatal("mutated durable input was accepted")
	}
}

func TestPrepareModelInputClassifiesDynamicCredentialButNotClinicalVocabulary(t *testing.T) {
	rendered := []byte("The patient model is used in healthcare workflow examples")
	base := ModelInputEnvelope{
		SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: 7,
		CanonicalProjectionDigest: SHA256Bytes([]byte("projection")),
		StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(rendered)}},
	}
	plain, err := PrepareModelInput(&base, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if containsModelInputClass(plain.Envelope.InputClassifications, string(modelegress.ClassificationClinical)) || containsModelInputClass(plain.Envelope.InputClassifications, string(modelegress.ClassificationSecret)) {
		t.Fatalf("ordinary clinical vocabulary was misclassified: %v", plain.Envelope.InputClassifications)
	}

	withSecret := cloneModelInputEnvelope(base)
	withSecret.VisibleHistory = []ModelInputMessage{{Role: ModelInputRoleAssistant, Content: "tool observation"}, {Role: ModelInputRoleAssistant, Content: "API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456"}}
	secret, err := PrepareModelInput(&withSecret, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if !containsModelInputClass(secret.Envelope.InputClassifications, string(modelegress.ClassificationSecret)) {
		t.Fatalf("dynamic credential did not classify the complete model input: %v", secret.Envelope.InputClassifications)
	}

	explicitClinical := cloneModelInputEnvelope(base)
	explicitClinical.InputClassifications = []string{string(modelegress.ClassificationClinical)}
	clinical, err := PrepareModelInput(&explicitClinical, modelInputSnapshot(rendered), rendered)
	if err != nil {
		t.Fatal(err)
	}
	if !containsModelInputClass(clinical.Envelope.InputClassifications, string(modelegress.ClassificationClinical)) {
		t.Fatal("explicit upstream clinical classification was discarded")
	}
}

func containsModelInputClass(classes []string, target string) bool {
	for _, class := range classes {
		if class == target {
			return true
		}
	}
	return false
}
