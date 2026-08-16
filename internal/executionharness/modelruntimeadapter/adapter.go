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
}

// Adapter deliberately enters Model Runtime through its two application
// services. It does not see stores, provider adapters, routing, egress,
// execution-identity, pricing, or wallet implementations.
type Adapter struct {
	invocations InvocationCreator
	dispatch    InvocationDispatcher
	clock       Clock
	config      Config
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
	config.RequiredCapabilities = append([]modelruntime.ModelCapability(nil), config.RequiredCapabilities...)
	if config.Temperature != nil {
		value := *config.Temperature
		config.Temperature = &value
	}
	return &Adapter{invocations: invocations, dispatch: dispatch, clock: clock, config: config}, nil
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
	envelope, err := modelInputEnvelope(contextSnapshotID, request, projection)
	if err != nil {
		return executionharness.ModelResult{}, err
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
		OutputMode:           modelruntime.OutputText,
		MaxOutputTokens:      a.config.MaxOutputTokens,
		Temperature:          cloneFloat(a.config.Temperature),
		ThinkingMode:         a.config.ThinkingMode,
		IdempotencyKey:       "execution-harness:" + request.CanonicalDigest,
		CorrelationID:        identity.CorrelationID,
		CausationID:          identity.CausationID,
		Deadline:             now.Add(a.config.InvocationTTL),
	})
	if err != nil {
		return executionharness.ModelResult{}, err
	}
	if err = validateCreatedInvocation(created.Invocation, identity, contextSnapshotID); err != nil {
		return executionharness.ModelResult{}, err
	}
	var dispatched modelruntime.DispatchResult
	if created.Reused {
		if created.Invocation.Status != modelruntime.InvocationSucceeded {
			return executionharness.ModelResult{}, fmt.Errorf("%w: reused invocation is not a completed outcome", ErrInvalidResult)
		}
		dispatched, err = a.invocations.Outcome(ctx, created.Invocation.ID)
	} else {
		dispatched, err = a.dispatch.Dispatch(ctx, created.Invocation.ID)
	}
	if err != nil {
		return executionharness.ModelResult{}, err
	}
	return mapDispatchResult(created.Invocation, dispatched)
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

func modelInputEnvelope(contextSnapshotID int64, request executionharness.NormalizedModelRequest, projection validatedProjection) (modelruntime.ModelInputEnvelope, error) {
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
	return modelruntime.ModelInputEnvelope{
		SchemaVersion:             modelruntime.ModelInputEnvelopeSchemaV1,
		ContextSnapshotID:         contextSnapshotID,
		CanonicalProjectionDigest: request.CanonicalDigest,
		StablePrefix:              []modelruntime.ModelInputMessage{{Role: modelruntime.ModelInputRoleUser, Content: projection.Prefix.Context.Content}},
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
	result.FinalOutput = dispatched.Result.TextOutput
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
