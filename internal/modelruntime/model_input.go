package modelruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contentpolicy"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

const ModelInputEnvelopeSchemaV1 = "modelruntime.input.v1"

const maxModelInputBytes = 8 << 20

type ModelInputRole string

const (
	ModelInputRoleSystem    ModelInputRole = "system"
	ModelInputRoleUser      ModelInputRole = "user"
	ModelInputRoleAssistant ModelInputRole = "assistant"
	ModelInputRoleTool      ModelInputRole = "tool"
)

type ModelInputToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ModelInputMessage struct {
	Role       ModelInputRole       `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	ToolName   string               `json:"tool_name,omitempty"`
	ToolCalls  []ModelInputToolCall `json:"tool_calls,omitempty"`
}

type ModelInputToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ModelInputEnvelope is the provider-independent input visible to one model
// invocation. It is not Harness history and it is not a ContextSnapshot. The
// first stable-prefix message is always the byte-exact rendered context of the
// referenced snapshot; later messages and tools are the immutable projection
// for this invocation.
type ModelInputEnvelope struct {
	SchemaVersion             string                     `json:"schema_version"`
	ContextSnapshotID         int64                      `json:"context_snapshot_id"`
	CanonicalProjectionDigest string                     `json:"canonical_projection_digest"`
	StablePrefix              []ModelInputMessage        `json:"stable_prefix"`
	StablePrefixDigest        string                     `json:"stable_prefix_digest"`
	VisibleHistory            []ModelInputMessage        `json:"visible_history"`
	ToolDefinitions           []ModelInputToolDefinition `json:"tool_definitions"`
	ProviderContinuationRef   string                     `json:"provider_continuation_ref,omitempty"`
	InputClassifications      []string                   `json:"input_classifications"`
	InputClassificationsHash  string                     `json:"input_classifications_hash"`
}

type PreparedModelInput struct {
	Envelope        ModelInputEnvelope
	CanonicalBytes  []byte
	CanonicalDigest string
}

var (
	modelInputCallIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
	modelInputDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// PrepareModelInput creates the immutable input record. supplied may be nil
// for legacy single-shot callers; those callers receive a one-message input
// envelope rather than a dispatch-time escape hatch around the new contract.
func PrepareModelInput(supplied *ModelInputEnvelope, snapshot ContextSnapshotRef, renderedContext []byte) (PreparedModelInput, error) {
	if snapshot.ID <= 0 || len(renderedContext) == 0 || SHA256Bytes(renderedContext) != snapshot.RenderedHash {
		return PreparedModelInput{}, fmt.Errorf("%w: context render is not bound to snapshot", ErrContextRejected)
	}
	var envelope ModelInputEnvelope
	if supplied == nil {
		envelope = ModelInputEnvelope{
			SchemaVersion:             ModelInputEnvelopeSchemaV1,
			ContextSnapshotID:         snapshot.ID,
			CanonicalProjectionDigest: SHA256Bytes(renderedContext),
			StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(renderedContext)}},
		}
	} else {
		envelope = cloneModelInputEnvelope(*supplied)
	}
	if envelope.SchemaVersion != ModelInputEnvelopeSchemaV1 || envelope.ContextSnapshotID != snapshot.ID {
		return PreparedModelInput{}, fmt.Errorf("%w: model input schema or context binding is invalid", ErrInvalidRequest)
	}
	if !modelInputDigestPattern.MatchString(envelope.CanonicalProjectionDigest) {
		return PreparedModelInput{}, fmt.Errorf("%w: projection digest is invalid", ErrInvalidRequest)
	}
	if len(envelope.StablePrefix) == 0 || envelope.StablePrefix[0].Role != ModelInputRoleUser || envelope.StablePrefix[0].Content != string(renderedContext) {
		return PreparedModelInput{}, fmt.Errorf("%w: stable prefix must begin with the exact context snapshot render", ErrContextRejected)
	}
	if err := normalizeModelInput(&envelope); err != nil {
		return PreparedModelInput{}, err
	}
	prefixBytes, err := canonicalStablePrefix(envelope)
	if err != nil {
		return PreparedModelInput{}, fmt.Errorf("%w: stable prefix is not canonicalizable", ErrInvalidRequest)
	}
	envelope.StablePrefixDigest = SHA256Bytes(prefixBytes)

	classes := append([]string(nil), snapshot.DataClasses...)
	classes = append(classes, envelope.InputClassifications...)
	if contentpolicy.Analyze(modelVisibleText(envelope)).HasCredentials() {
		classes = append(classes, string(modelegress.ClassificationSecret))
	}
	classes, classesHash := modelegress.NormalizeClassifications(classes)
	for _, class := range classes {
		if !validModelInputClassification(class) {
			return PreparedModelInput{}, fmt.Errorf("%w: model input classification %q is invalid", ErrInvalidRequest, class)
		}
	}
	envelope.InputClassifications = classes
	envelope.InputClassificationsHash = classesHash
	canonical, err := CanonicalJSON(envelope)
	if err != nil {
		return PreparedModelInput{}, fmt.Errorf("%w: model input cannot be canonicalized", ErrInvalidRequest)
	}
	if len(canonical) > maxModelInputBytes {
		return PreparedModelInput{}, fmt.Errorf("%w: model input exceeds maximum size", ErrInvalidRequest)
	}
	return PreparedModelInput{Envelope: envelope, CanonicalBytes: canonical, CanonicalDigest: SHA256Bytes(canonical)}, nil
}

func ValidateStoredModelInput(stored PreparedModelInput, snapshot ContextSnapshotRef) (PreparedModelInput, error) {
	if len(stored.CanonicalBytes) == 0 || !modelInputDigestPattern.MatchString(stored.CanonicalDigest) || SHA256Bytes(stored.CanonicalBytes) != stored.CanonicalDigest {
		return PreparedModelInput{}, fmt.Errorf("%w: durable model input digest mismatch", ErrContextRejected)
	}
	var decoded ModelInputEnvelope
	if err := json.Unmarshal(stored.CanonicalBytes, &decoded); err != nil {
		return PreparedModelInput{}, fmt.Errorf("%w: durable model input is invalid", ErrContextRejected)
	}
	if decoded.SchemaVersion != ModelInputEnvelopeSchemaV1 || decoded.ContextSnapshotID != snapshot.ID || !modelInputDigestPattern.MatchString(decoded.CanonicalProjectionDigest) {
		return PreparedModelInput{}, fmt.Errorf("%w: durable model input scope is invalid", ErrContextRejected)
	}
	if len(decoded.StablePrefix) == 0 || decoded.StablePrefix[0].Role != ModelInputRoleUser || SHA256Bytes([]byte(decoded.StablePrefix[0].Content)) != snapshot.RenderedHash {
		return PreparedModelInput{}, fmt.Errorf("%w: durable model input is not bound to context render", ErrContextRejected)
	}
	claimedPrefixDigest := decoded.StablePrefixDigest
	claimedClassesHash := decoded.InputClassificationsHash
	if err := normalizeModelInput(&decoded); err != nil {
		return PreparedModelInput{}, err
	}
	prefixBytes, err := canonicalStablePrefix(decoded)
	if err != nil || SHA256Bytes(prefixBytes) != claimedPrefixDigest {
		return PreparedModelInput{}, fmt.Errorf("%w: durable stable prefix digest mismatch", ErrContextRejected)
	}
	classes := append([]string(nil), decoded.InputClassifications...)
	if contentpolicy.Analyze(modelVisibleText(decoded)).HasCredentials() {
		classes = append(classes, string(modelegress.ClassificationSecret))
	}
	classes, classesHash := modelegress.NormalizeClassifications(classes)
	for _, class := range classes {
		if !validModelInputClassification(class) {
			return PreparedModelInput{}, fmt.Errorf("%w: durable model input classification %q is invalid", ErrContextRejected, class)
		}
	}
	if classesHash != claimedClassesHash || !equalModelInputStrings(classes, decoded.InputClassifications) {
		return PreparedModelInput{}, fmt.Errorf("%w: durable input classifications mismatch", ErrContextRejected)
	}
	rebuiltBytes, err := CanonicalJSON(decoded)
	if err != nil {
		return PreparedModelInput{}, fmt.Errorf("%w: durable model input cannot be canonicalized", ErrContextRejected)
	}
	if SHA256Bytes(rebuiltBytes) != stored.CanonicalDigest || !bytes.Equal(rebuiltBytes, stored.CanonicalBytes) {
		return PreparedModelInput{}, fmt.Errorf("%w: durable model input is not canonical", ErrContextRejected)
	}
	return PreparedModelInput{Envelope: decoded, CanonicalBytes: rebuiltBytes, CanonicalDigest: stored.CanonicalDigest}, nil
}

func ValidateModelInputContextRender(input PreparedModelInput, renderedContext []byte) error {
	if len(input.Envelope.StablePrefix) == 0 || input.Envelope.StablePrefix[0].Content != string(renderedContext) {
		return fmt.Errorf("%w: model input context bytes drifted", ErrContextRejected)
	}
	return nil
}

func isLegacySingleShotModelInput(input PreparedModelInput, renderedContext []byte) bool {
	return len(input.Envelope.StablePrefix) == 1 && input.Envelope.StablePrefix[0].Role == ModelInputRoleUser &&
		input.Envelope.StablePrefix[0].Content == string(renderedContext) && len(input.Envelope.VisibleHistory) == 0 &&
		len(input.Envelope.ToolDefinitions) == 0 && input.Envelope.ProviderContinuationRef == "" &&
		input.Envelope.CanonicalProjectionDigest == SHA256Bytes(renderedContext)
}

func normalizeModelInput(envelope *ModelInputEnvelope) error {
	if len(envelope.ProviderContinuationRef) > 4096 {
		return fmt.Errorf("%w: provider continuation reference is too large", ErrInvalidRequest)
	}
	seenCalls := make(map[string]struct{})
	for i := range envelope.StablePrefix {
		if err := normalizeModelInputMessage(&envelope.StablePrefix[i], false, seenCalls); err != nil {
			return err
		}
		if envelope.StablePrefix[i].Role != ModelInputRoleSystem && envelope.StablePrefix[i].Role != ModelInputRoleUser {
			return fmt.Errorf("%w: stable prefix contains non-prefix role", ErrInvalidRequest)
		}
	}
	for i := range envelope.VisibleHistory {
		if err := normalizeModelInputMessage(&envelope.VisibleHistory[i], true, seenCalls); err != nil {
			return err
		}
	}
	seenTools := make(map[string]struct{}, len(envelope.ToolDefinitions))
	for i := range envelope.ToolDefinitions {
		tool := &envelope.ToolDefinitions[i]
		tool.Name = strings.TrimSpace(tool.Name)
		tool.Description = strings.TrimSpace(tool.Description)
		if !toolNamePattern.MatchString(tool.Name) {
			return fmt.Errorf("%w: invalid model tool name", ErrInvalidRequest)
		}
		if _, duplicate := seenTools[tool.Name]; duplicate {
			return fmt.Errorf("%w: duplicate model tool definition", ErrInvalidRequest)
		}
		seenTools[tool.Name] = struct{}{}
		canonical, err := CanonicalizeRawJSON(tool.InputSchema)
		if err != nil || len(canonical) == 0 {
			return fmt.Errorf("%w: invalid model tool schema", ErrInvalidRequest)
		}
		value, err := decodeJSONWithNumbers(canonical)
		definition, ok := value.(map[string]any)
		if err != nil || !ok || definition["type"] != "object" || validateSchemaDefinition(definition, 0) != nil {
			return fmt.Errorf("%w: model tool schema must be a supported object schema", ErrInvalidRequest)
		}
		tool.InputSchema = canonical
	}
	if !sort.SliceIsSorted(envelope.ToolDefinitions, func(i, j int) bool { return envelope.ToolDefinitions[i].Name < envelope.ToolDefinitions[j].Name }) {
		sort.Slice(envelope.ToolDefinitions, func(i, j int) bool { return envelope.ToolDefinitions[i].Name < envelope.ToolDefinitions[j].Name })
	}
	return nil
}

func normalizeModelInputMessage(message *ModelInputMessage, history bool, seenCalls map[string]struct{}) error {
	switch message.Role {
	case ModelInputRoleSystem, ModelInputRoleUser:
		if history || message.Content == "" || message.ToolCallID != "" || message.ToolName != "" || len(message.ToolCalls) != 0 {
			return fmt.Errorf("%w: invalid system/user model input message", ErrInvalidRequest)
		}
	case ModelInputRoleAssistant:
		if !history || (message.Content == "" && len(message.ToolCalls) == 0) || message.ToolCallID != "" || message.ToolName != "" {
			return fmt.Errorf("%w: invalid assistant model input message", ErrInvalidRequest)
		}
		for i := range message.ToolCalls {
			call := &message.ToolCalls[i]
			call.ID = strings.TrimSpace(call.ID)
			call.Name = strings.TrimSpace(call.Name)
			if !modelInputCallIDPattern.MatchString(call.ID) || !toolNamePattern.MatchString(call.Name) {
				return fmt.Errorf("%w: invalid model tool call", ErrInvalidRequest)
			}
			if _, duplicate := seenCalls[call.ID]; duplicate {
				return fmt.Errorf("%w: duplicate model tool call ID", ErrInvalidRequest)
			}
			seenCalls[call.ID] = struct{}{}
			args, err := CanonicalizeRawJSON(call.Arguments)
			if err != nil || len(args) == 0 {
				return fmt.Errorf("%w: invalid model tool call arguments", ErrInvalidRequest)
			}
			call.Arguments = args
		}
	case ModelInputRoleTool:
		message.ToolCallID = strings.TrimSpace(message.ToolCallID)
		message.ToolName = strings.TrimSpace(message.ToolName)
		if !history || message.Content == "" || !modelInputCallIDPattern.MatchString(message.ToolCallID) || !toolNamePattern.MatchString(message.ToolName) || len(message.ToolCalls) != 0 {
			return fmt.Errorf("%w: invalid tool result model input message", ErrInvalidRequest)
		}
		if _, exists := seenCalls[message.ToolCallID]; !exists {
			return fmt.Errorf("%w: tool result has no preceding call", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: invalid model input role", ErrInvalidRequest)
	}
	return nil
}

func modelVisibleText(envelope ModelInputEnvelope) string {
	var text strings.Builder
	appendMessage := func(message ModelInputMessage) {
		text.WriteString(message.Content)
		text.WriteByte('\n')
		text.WriteString(message.ToolCallID)
		text.WriteByte('\n')
		text.WriteString(message.ToolName)
		text.WriteByte('\n')
		for _, call := range message.ToolCalls {
			text.WriteString(call.ID)
			text.WriteByte('\n')
			text.WriteString(call.Name)
			text.WriteByte('\n')
			text.Write(call.Arguments)
			text.WriteByte('\n')
		}
	}
	for _, message := range envelope.StablePrefix {
		appendMessage(message)
	}
	for _, message := range envelope.VisibleHistory {
		appendMessage(message)
	}
	for _, tool := range envelope.ToolDefinitions {
		text.WriteString(tool.Name)
		text.WriteByte('\n')
		text.WriteString(tool.Description)
		text.WriteByte('\n')
		text.Write(tool.InputSchema)
		text.WriteByte('\n')
	}
	text.WriteString(envelope.ProviderContinuationRef)
	return text.String()
}

// canonicalStablePrefix binds both prefix messages and tool definitions.
// Tools are provider-visible action-space declarations and therefore part of
// the stable prefix even though the envelope stores them in a distinct field.
func canonicalStablePrefix(envelope ModelInputEnvelope) ([]byte, error) {
	return CanonicalJSON(struct {
		Messages []ModelInputMessage        `json:"messages"`
		Tools    []ModelInputToolDefinition `json:"tools"`
	}{Messages: envelope.StablePrefix, Tools: envelope.ToolDefinitions})
}

func validModelInputClassification(value string) bool {
	switch modelegress.DataClassification(value) {
	case modelegress.ClassificationPublic, modelegress.ClassificationSanitized, modelegress.ClassificationOrganizational, modelegress.ClassificationSecret, modelegress.ClassificationClinical:
		return true
	default:
		return false
	}
}

func cloneModelInputEnvelope(input ModelInputEnvelope) ModelInputEnvelope {
	body, _ := json.Marshal(input)
	var clone ModelInputEnvelope
	_ = json.Unmarshal(body, &clone)
	return clone
}

func equalModelInputStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
