// Package modelruntimeadapter connects the provider-independent Execution
// Harness to the existing Model Runtime invocation and dispatch boundary.
package modelruntimeadapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

const harnessRequestSchemaV1 = "executionharness.request.v1"

var (
	ErrInvalidProjection = errors.New("invalid execution harness model projection")
	ErrBindingDrift      = errors.New("model runtime invocation binding drift")
	ErrInvalidResult     = errors.New("invalid model runtime dispatch result")
)

type InvocationCreator interface {
	Create(context.Context, modelruntime.CreateInvocationCommand) (modelruntime.CreateInvocationResult, error)
	Outcome(context.Context, int64) (modelruntime.DispatchResult, error)
	FindIdempotent(context.Context, string) (modelruntime.Invocation, modelruntime.PreparedModelInput, error)
}

type InvocationDispatcher interface {
	Dispatch(context.Context, int64) (modelruntime.DispatchResult, error)
}

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type Config struct {
	RequiredCapabilities []modelruntime.ModelCapability
	MaxOutputTokens      int
	Temperature          *float64
	ThinkingMode         modelruntime.ThinkingMode
	InvocationTTL        time.Duration
	// OutputMode and OutputSchema are the run's output contract. They default
	// to free text, which is what a tool-calling trajectory wants. A consumer
	// that needs a structured answer -- the Executive validates every model
	// result against a per-task JSON schema -- sets them here rather than
	// post-parsing whatever text came back.
	OutputMode   modelruntime.OutputMode
	OutputSchema json.RawMessage
	// ExecutionContractInstructions is optional execution-time contract
	// guidance appended to the model's stable prefix as its own user message,
	// after the byte-exact context snapshot render. It exists so a caller can
	// deliver an output-contract instruction to the model even when the durable
	// context (e.g. a task created before the instruction existed) does not
	// carry it. It never modifies the context snapshot render: StablePrefix[0]
	// stays byte-exact, and this instruction is a separate, provider-visible
	// message. Empty means no additional instruction (legacy behavior).
	ExecutionContractInstructions string
}

// Adapter deliberately enters Model Runtime through its two application
// services. It does not see stores, provider adapters, routing, egress,
// execution-identity, pricing, or wallet implementations.
type Adapter struct {
	invocations InvocationCreator
	dispatch    InvocationDispatcher
	clock       Clock
	config      Config
	// outputContract is the digest of the output contract this adapter will
	// create invocations under. It participates in the idempotency key so a
	// re-entry under a different contract cannot silently adopt an invocation
	// created under the previous one.
	outputContract string
	// executionContractKey is the digest of the execution-contract
	// instructions this adapter delivers with the model input, or empty when
	// the run carries none. Like outputContract, it participates in the
	// idempotency key: a re-entry under different execution-contract
	// instructions must not silently adopt an invocation created under other
	// instructions. It is deliberately NOT folded into outputContract, which
	// is re-derived from the durable invocation row on reuse and therefore
	// can only contain facts that row persists. Empty keeps the historical
	// key shape byte-identical for runs without an execution contract.
	executionContractKey string
}

var _ executionharness.ModelExecutor = (*Adapter)(nil)

func New(invocations InvocationCreator, dispatch InvocationDispatcher, clock Clock, config Config) (*Adapter, error) {
	if invocations == nil || dispatch == nil {
		return nil, errors.New("harness model runtime adapter dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	if config.MaxOutputTokens < 1 || config.InvocationTTL <= 0 || config.InvocationTTL > 24*time.Hour {
		return nil, errors.New("harness model runtime adapter configuration is invalid")
	}
	switch config.ThinkingMode {
	case modelruntime.ThinkingDisabled, modelruntime.ThinkingOpaque:
	default:
		return nil, errors.New("harness model runtime adapter thinking mode is invalid")
	}
	if config.OutputMode == "" {
		config.OutputMode = modelruntime.OutputText
	}
	switch config.OutputMode {
	case modelruntime.OutputText:
		if len(config.OutputSchema) > 0 {
			return nil, errors.New("harness model runtime adapter text output must not carry a schema")
		}
	case modelruntime.OutputJSON:
		if len(config.OutputSchema) == 0 {
			return nil, errors.New("harness model runtime adapter json output requires a schema")
		}
		// Canonicalized once, here, with Model Runtime's own primitive: the
		// schema that is hashed must be byte-identical to the schema that is
		// persisted, or the durable comparison below would be comparing two
		// different renderings of the same document.
		canonical, err := modelruntime.CanonicalizeRawJSON(config.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("canonicalize harness output schema: %w", err)
		}
		config.OutputSchema = canonical
	default:
		return nil, errors.New("harness model runtime adapter output mode is invalid")
	}
	// Model Runtime trims, de-duplicates and sorts capabilities before it
	// persists them. Hashing the caller's raw slice instead would make a
	// perfectly valid configuration produce one digest on creation and a
	// different one when recomputed from the durable row, turning a correct
	// reuse into a false binding drift.
	config.RequiredCapabilities = modelruntime.NormalizeCapabilities(config.RequiredCapabilities)
	if config.Temperature != nil {
		value := *config.Temperature
		config.Temperature = &value
	}
	contract, err := outputContractDigest(config.OutputMode, config.OutputSchema, config.MaxOutputTokens, config.Temperature, config.ThinkingMode, config.RequiredCapabilities)
	if err != nil {
		return nil, err
	}
	// The execution-contract key component exists exactly when the delivered
	// model input carries the contract message (same TrimSpace predicate as
	// modelInputEnvelope), so key shape and model input shape cannot drift.
	executionContractKey := ""
	if strings.TrimSpace(config.ExecutionContractInstructions) != "" {
		executionContractKey = digest([]byte(config.ExecutionContractInstructions))
	}
	return &Adapter{invocations: invocations, dispatch: dispatch, clock: clock, config: config, outputContract: contract, executionContractKey: executionContractKey}, nil
}

// outputContractDigest is a stable digest of everything that decides what the
// provider is asked to produce. The harness projection digest describes the
// conversation; this describes the answer contract. Both belong in the
// idempotency key, because two runs with the same conversation and different
// answer contracts are not the same invocation.
func outputContractDigest(mode modelruntime.OutputMode, schema json.RawMessage, maxOutputTokens int, temperature *float64, thinking modelruntime.ThinkingMode, capabilities []modelruntime.ModelCapability) (string, error) {
	body, err := modelruntime.CanonicalJSON(struct {
		OutputMode      modelruntime.OutputMode        `json:"output_mode"`
		OutputSchema    string                         `json:"output_schema"`
		MaxOutputTokens int                            `json:"max_output_tokens"`
		Temperature     *float64                       `json:"temperature"`
		ThinkingMode    modelruntime.ThinkingMode      `json:"thinking_mode"`
		Capabilities    []modelruntime.ModelCapability `json:"required_capabilities"`
	}{mode, string(schema), maxOutputTokens, temperature, thinking, modelruntime.NormalizeCapabilities(capabilities)})
	if err != nil {
		return "", fmt.Errorf("canonicalize harness output contract: %w", err)
	}
	return modelruntime.SHA256Bytes(body), nil
}

func (a *Adapter) Invoke(ctx context.Context, identity executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	projection, err := validateProjection(identity, request)
	if err != nil {
		return executionharness.ModelResult{}, err
	}
	contextSnapshotID, err := strconv.ParseInt(projection.Prefix.Context.ID, 10, 64)
	if err != nil || contextSnapshotID <= 0 {
		return executionharness.ModelResult{}, fmt.Errorf("%w: initial context ID must be a positive model runtime snapshot ID", ErrInvalidProjection)
	}
	envelope, err := modelInputEnvelope(contextSnapshotID, request, projection, a.config.ExecutionContractInstructions)
	if err != nil {
		return executionharness.ModelResult{}, err
	}
	idempotencyKey := a.idempotencyKey(request.CanonicalDigest)
	if existing, stored, findErr := a.invocations.FindIdempotent(ctx, idempotencyKey); findErr == nil {
		if err = a.validateExistingInvocation(existing, stored, identity, contextSnapshotID, request.CanonicalDigest); err != nil {
			return executionharness.ModelResult{}, err
		}
		return a.resume(ctx, existing)
	} else if !errors.Is(findErr, modelruntime.ErrNotFound) {
		return executionharness.ModelResult{}, findErr
	}
	now := a.clock.Now().UTC()
	created, err := a.invocations.Create(ctx, modelruntime.CreateInvocationCommand{
		OrganizationID:       identity.OrganizationID,
		TaskID:               identity.TaskID,
		AttemptID:            identity.AttemptID,
		SubjectRoleID:        identity.RoleID,
		ContextSnapshotID:    contextSnapshotID,
		ModelInput:           &envelope,
		Purpose:              "execution harness turn " + request.CanonicalDigest,
		RequiredCapabilities: append([]modelruntime.ModelCapability(nil), a.config.RequiredCapabilities...),
		OutputMode:           a.config.OutputMode,
		OutputSchema:         append(json.RawMessage(nil), a.config.OutputSchema...),
		MaxOutputTokens:      a.config.MaxOutputTokens,
		Temperature:          cloneFloat(a.config.Temperature),
		ThinkingMode:         a.config.ThinkingMode,
		IdempotencyKey:       idempotencyKey,
		CorrelationID:        identity.CorrelationID,
		CausationID:          identity.CausationID,
		Deadline:             now.Add(a.config.InvocationTTL),
	})
	if err != nil {
		if errors.Is(err, modelruntime.ErrConflict) {
			existing, stored, findErr := a.invocations.FindIdempotent(ctx, idempotencyKey)
			if findErr == nil {
				if validateErr := a.validateExistingInvocation(existing, stored, identity, contextSnapshotID, request.CanonicalDigest); validateErr != nil {
					return executionharness.ModelResult{}, validateErr
				}
				return a.resume(ctx, existing)
			}
		}
		return executionharness.ModelResult{}, err
	}
	if err = validateCreatedInvocation(created.Invocation, identity, contextSnapshotID); err != nil {
		return executionharness.ModelResult{}, err
	}
	if created.Reused {
		return a.resume(ctx, created.Invocation)
	}
	dispatched, err := a.dispatch.Dispatch(ctx, created.Invocation.ID)
	if err != nil {
		// The invocation exists and Model Runtime has already recorded
		// whether its failure was transient. Returning a bare error here
		// threw away the only reference by which that answer can be
		// retrieved.
		return executionharness.ModelResult{}, executionharness.WithInvocationRef(
			strconv.FormatInt(created.Invocation.ID, 10), err)
	}
	return mapDispatchResult(created.Invocation, dispatched)
}

func (a *Adapter) resume(ctx context.Context, invocation modelruntime.Invocation) (executionharness.ModelResult, error) {
	var (
		dispatched modelruntime.DispatchResult
		err        error
	)
	switch invocation.Status {
	case modelruntime.InvocationRequested:
		dispatched, err = a.dispatch.Dispatch(ctx, invocation.ID)
	case modelruntime.InvocationSucceeded:
		dispatched, err = a.invocations.Outcome(ctx, invocation.ID)
	default:
		return executionharness.ModelResult{}, fmt.Errorf("%w: existing invocation is not safely resumable", ErrInvalidResult)
	}
	if err != nil {
		return executionharness.ModelResult{}, executionharness.WithInvocationRef(
			strconv.FormatInt(invocation.ID, 10), err)
	}
	return mapDispatchResult(invocation, dispatched)
}

type harnessWire struct {
	SchemaVersion           string                       `json:"schema_version"`
	RunIdentity             executionharness.RunIdentity `json:"run_identity"`
	StablePrefix            json.RawMessage              `json:"stable_prefix"`
	VisibleHistory          []executionharness.Message   `json:"visible_history"`
	ProviderContinuationRef string                       `json:"provider_continuation_ref,omitempty"`
}

type harnessPrefix struct {
	Context executionharness.InitialContext   `json:"initial_context"`
	Tools   []executionharness.ToolDefinition `json:"tools"`
	Policy  executionharness.RunPolicy        `json:"policy"`
}

type validatedProjection struct {
	Wire   harnessWire
	Prefix harnessPrefix
}

func validateProjection(identity executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (validatedProjection, error) {
	if request.RunIdentity != identity {
		return validatedProjection{}, fmt.Errorf("%w: call identity and request identity differ", ErrBindingDrift)
	}
	if len(request.CanonicalBytes) == 0 || digest(request.CanonicalBytes) != request.CanonicalDigest {
		return validatedProjection{}, fmt.Errorf("%w: canonical request digest mismatch", ErrInvalidProjection)
	}
	var wire harnessWire
	if err := decodeExact(request.CanonicalBytes, &wire); err != nil {
		return validatedProjection{}, fmt.Errorf("%w: canonical request: %v", ErrInvalidProjection, err)
	}
	canonical, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(canonical, request.CanonicalBytes) {
		return validatedProjection{}, fmt.Errorf("%w: request bytes are not canonical", ErrInvalidProjection)
	}
	if wire.SchemaVersion != harnessRequestSchemaV1 || wire.RunIdentity != identity ||
		!bytes.Equal(wire.StablePrefix, request.StablePrefix) ||
		wire.ProviderContinuationRef != request.ProviderContinuationRef ||
		!equalMessages(wire.VisibleHistory, request.VisibleHistory) {
		return validatedProjection{}, fmt.Errorf("%w: request fields drift from canonical bytes", ErrBindingDrift)
	}
	var prefix harnessPrefix
	if err = decodeExact(request.StablePrefix, &prefix); err != nil {
		return validatedProjection{}, fmt.Errorf("%w: stable prefix: %v", ErrInvalidProjection, err)
	}
	prefixBytes, err := json.Marshal(prefix)
	if err != nil || !bytes.Equal(prefixBytes, request.StablePrefix) {
		return validatedProjection{}, fmt.Errorf("%w: stable prefix is not canonical", ErrInvalidProjection)
	}
	return validatedProjection{Wire: wire, Prefix: prefix}, nil
}

func modelInputEnvelope(contextSnapshotID int64, request executionharness.NormalizedModelRequest, projection validatedProjection, executionContract string) (modelruntime.ModelInputEnvelope, error) {
	tools := make([]modelruntime.ModelInputToolDefinition, len(projection.Prefix.Tools))
	for i, tool := range projection.Prefix.Tools {
		tools[i] = modelruntime.ModelInputToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: append([]byte(nil), tool.InputSchema...)}
	}
	history := make([]modelruntime.ModelInputMessage, 0, len(projection.Wire.VisibleHistory))
	for _, message := range projection.Wire.VisibleHistory {
		switch message.Role {
		case "assistant":
			calls := make([]modelruntime.ModelInputToolCall, len(message.ToolRequests))
			for i, call := range message.ToolRequests {
				calls[i] = modelruntime.ModelInputToolCall{ID: call.ToolCallID, Name: call.ToolName, Arguments: append([]byte(nil), call.Arguments...)}
			}
			history = append(history, modelruntime.ModelInputMessage{Role: modelruntime.ModelInputRoleAssistant, Content: message.Content, ToolCalls: calls})
		case "tool":
			content, err := toolResultContent(message)
			if err != nil {
				return modelruntime.ModelInputEnvelope{}, err
			}
			history = append(history, modelruntime.ModelInputMessage{Role: modelruntime.ModelInputRoleTool, Content: content, ToolCallID: message.ToolCallID, ToolName: message.ToolName})
		default:
			return modelruntime.ModelInputEnvelope{}, fmt.Errorf("%w: unsupported visible history role", ErrInvalidProjection)
		}
	}
	// The first stable-prefix message is always the byte-exact context snapshot
	// render; Model Runtime binds the invocation to it. Any execution-contract
	// guidance is appended as a separate user message AFTER it, so the snapshot
	// render's content/digest/provenance is never rewritten while the contract
	// still reaches the model at execution time.
	stablePrefix := []modelruntime.ModelInputMessage{{Role: modelruntime.ModelInputRoleUser, Content: projection.Prefix.Context.Content}}
	if strings.TrimSpace(executionContract) != "" {
		stablePrefix = append(stablePrefix, modelruntime.ModelInputMessage{Role: modelruntime.ModelInputRoleUser, Content: executionContract})
	}
	return modelruntime.ModelInputEnvelope{
		SchemaVersion:             modelruntime.ModelInputEnvelopeSchemaV1,
		ContextSnapshotID:         contextSnapshotID,
		CanonicalProjectionDigest: request.CanonicalDigest,
		StablePrefix:              stablePrefix,
		VisibleHistory:            history,
		ToolDefinitions:           tools,
		ProviderContinuationRef:   request.ProviderContinuationRef,
	}, nil
}

func toolResultContent(message executionharness.Message) (string, error) {
	if message.ToolError != "" {
		body, err := json.Marshal(struct {
			ErrorCode string `json:"error_code"`
		}{ErrorCode: message.ToolError})
		return string(body), err
	}
	if len(message.ToolResult) == 0 {
		return "", fmt.Errorf("%w: tool result content is empty", ErrInvalidProjection)
	}
	var value any
	if err := decodeExact(message.ToolResult, &value); err != nil {
		return "", fmt.Errorf("%w: tool result is not canonical JSON", ErrInvalidProjection)
	}
	body, err := json.Marshal(value)
	if err != nil || !bytes.Equal(body, message.ToolResult) {
		return "", fmt.Errorf("%w: tool result is not canonical JSON", ErrInvalidProjection)
	}
	return string(body), nil
}

func validateCreatedInvocation(invocation modelruntime.Invocation, identity executionharness.RunIdentity, contextSnapshotID int64) error {
	if invocation.ID <= 0 || invocation.OrganizationID != identity.OrganizationID || invocation.TaskID != identity.TaskID ||
		invocation.AttemptID != identity.AttemptID || invocation.SubjectRoleID != identity.RoleID ||
		invocation.ContextSnapshotID != contextSnapshotID || invocation.CorrelationID != identity.CorrelationID ||
		invocation.CausationID != identity.CausationID || invocation.DispatcherAssignmentID == nil || invocation.ExecutionPrincipalID == nil {
		return ErrBindingDrift
	}
	return nil
}

func (a *Adapter) validateExistingInvocation(invocation modelruntime.Invocation, input modelruntime.PreparedModelInput, identity executionharness.RunIdentity, contextSnapshotID int64, projectionDigest string) error {
	if err := validateCreatedInvocation(invocation, identity, contextSnapshotID); err != nil {
		return err
	}
	// The idempotency key already carries the contract digest, so a mismatch
	// here means the durable row disagrees with the key that found it. Compare
	// against persisted state rather than trusting the key alone: a key is a
	// lookup, not a guarantee about what was stored.
	stored, err := outputContractDigest(invocation.OutputMode, invocation.OutputSchema, invocation.MaxOutputTokens, invocation.Temperature, invocation.ThinkingMode, invocation.RequiredCapabilities)
	if err != nil {
		return err
	}
	if stored != a.outputContract {
		return fmt.Errorf("%w: durable invocation was created under a different output contract", ErrBindingDrift)
	}
	if input.Envelope.SchemaVersion != modelruntime.ModelInputEnvelopeSchemaV1 || input.Envelope.ContextSnapshotID != contextSnapshotID ||
		input.Envelope.CanonicalProjectionDigest != projectionDigest || input.CanonicalDigest == "" || digest(input.CanonicalBytes) != input.CanonicalDigest {
		return fmt.Errorf("%w: durable model input does not match the harness projection", ErrBindingDrift)
	}
	return nil
}

func mapDispatchResult(created modelruntime.Invocation, dispatched modelruntime.DispatchResult) (executionharness.ModelResult, error) {
	if dispatched.Invocation.ID != created.ID || dispatched.Invocation.OrganizationID != created.OrganizationID ||
		dispatched.Invocation.TaskID != created.TaskID || dispatched.Invocation.AttemptID != created.AttemptID ||
		dispatched.Invocation.SubjectRoleID != created.SubjectRoleID || dispatched.Invocation.Status != modelruntime.InvocationSucceeded ||
		dispatched.Result == nil || dispatched.Usage == nil || dispatched.Result.InvocationID != created.ID || dispatched.Usage.InvocationID != created.ID {
		return executionharness.ModelResult{}, ErrInvalidResult
	}
	result := executionharness.ModelResult{InvocationRef: strconv.FormatInt(created.ID, 10)}
	result.Usage = executionharness.Usage{
		InputTokens:       int64Pointer(dispatched.Usage.InputTokens),
		OutputTokens:      int64Pointer(dispatched.Usage.OutputTokens),
		CachedInputTokens: cloneInt64(dispatched.Usage.PromptCacheHitTokens),
	}
	if len(dispatched.Result.ToolIntents) > 0 {
		result.FinishReason = executionharness.FinishTools
		result.ToolRequests = make([]executionharness.ToolRequest, len(dispatched.Result.ToolIntents))
		for i, intent := range dispatched.Result.ToolIntents {
			if strings.TrimSpace(intent.ID) == "" {
				return executionharness.ModelResult{}, fmt.Errorf("%w: provider tool request lacks stable call ID", ErrInvalidResult)
			}
			result.ToolRequests[i] = executionharness.ToolRequest{ToolCallID: intent.ID, ToolName: intent.Name, Arguments: append([]byte(nil), intent.Arguments...)}
		}
		return result, nil
	}
	result.FinishReason = executionharness.FinishFinal
	switch created.OutputMode {
	case modelruntime.OutputJSON:
		// Model Runtime already canonicalized this on the way in; the Harness
		// carries the canonical bytes through unchanged so the consumer parses
		// exactly what was persisted and hashed.
		if len(dispatched.Result.JSONOutput) == 0 {
			return executionharness.ModelResult{}, fmt.Errorf("%w: json output contract produced no canonical JSON", ErrInvalidResult)
		}
		result.FinalOutput = string(dispatched.Result.JSONOutput)
	default:
		result.FinalOutput = dispatched.Result.TextOutput
	}
	return result, nil
}

func decodeExact(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func equalMessages(left, right []executionharness.Message) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

// idempotencyKey is bounded without changing what a historical run already
// wrote down.
//
// The legacy representation concatenated the namespace prefix with each
// 64-character digest. Without an execution contract that is 18 + 64 + 1 + 64
// = 147 bytes, which was always inside Model Runtime's 200-byte bound. WITH
// one it becomes 212, past the bound, so the invocation is rejected before it
// is ever created -- and the execution contract is injected for exactly two
// purposes, department planning and department review, which is why those two
// alone could never dispatch through the Harness.
//
// Only the oversized case is compacted. Rewriting the compatible case too
// would have been tidier and was wrong: Invoke resolves an existing
// invocation through FindIdempotent BEFORE creating one, so a runtime that
// derives a different key for the same work stops finding invocations that
// earlier runtimes durably created, and creates a second one instead. That is
// the restart-duplication failure this system already closed once, and a
// cosmetic change to a key that was never too long is not worth reopening it.
func (a *Adapter) idempotencyKey(canonicalDigest string) string {
	if a.executionContractKey == "" {
		// Byte-for-byte the historical shape, so every invocation created by
		// an earlier runtime is still adoptable by this one.
		return "execution-harness:" + canonicalDigest + ":" + a.outputContract
	}
	// The three-component form is the only one that exceeds the bound. Folding
	// keeps every component participating -- same inputs, same key; any change,
	// a different key -- at a fixed 82 bytes.
	return "execution-harness:" + digest([]byte(
		canonicalDigest+"\x00"+a.outputContract+"\x00"+a.executionContractKey,
	))
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func int64Pointer(value int64) *int64 { return &value }

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
