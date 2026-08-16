package executionharness

import (
	"encoding/json"
	"fmt"
)

// Project is a pure projection: no clock, I/O, random source, provider, DB, or
// mutable process state participates in its result.
func Project(spec RunSpec, history []Event) (NormalizedModelRequest, error) {
	tools, identityDigest, err := validateSpec(spec)
	if err != nil {
		return NormalizedModelRequest{}, err
	}
	if err := validateHistory(spec.Identity.RunID, history, identityDigest); err != nil {
		return NormalizedModelRequest{}, err
	}
	prefix, err := json.Marshal(struct {
		Context InitialContext   `json:"initial_context"`
		Tools   []ToolDefinition `json:"tools"`
		Policy  RunPolicy        `json:"policy"`
	}{spec.Context, tools, spec.Policy})
	if err != nil {
		return NormalizedModelRequest{}, err
	}
	messages := make([]Message, 0)
	continuation := ""
	for _, event := range history {
		switch event.Type {
		case EventModelResponseRecorded:
			if event.ModelResult == nil {
				return NormalizedModelRequest{}, fmt.Errorf("%w: model response without result", ErrHistoryCorrupt)
			}
			messages = append(messages, Message{Role: "assistant", Content: event.ModelResult.FinalOutput, ToolRequests: cloneToolRequests(event.ModelResult.ToolRequests)})
			if event.ModelResult.ReasoningTelemetry.OpaqueContinuationRef != "" {
				continuation = event.ModelResult.ReasoningTelemetry.OpaqueContinuationRef
			}
		case EventToolResultRecorded:
			if event.ToolRequest == nil {
				return NormalizedModelRequest{}, fmt.Errorf("%w: tool result without request", ErrHistoryCorrupt)
			}
			messages = append(messages, Message{Role: "tool", ToolCallID: event.ToolRequest.ToolCallID, ToolName: event.ToolRequest.ToolName, ToolResult: append([]byte(nil), event.ToolResult...)})
		case EventToolCallDenied:
			if event.ToolRequest != nil {
				messages = append(messages, Message{Role: "tool", ToolCallID: event.ToolRequest.ToolCallID, ToolName: event.ToolRequest.ToolName, ToolError: event.ErrorCode})
			}
		}
	}
	wire := struct {
		SchemaVersion           string          `json:"schema_version"`
		RunIdentity             RunIdentity     `json:"run_identity"`
		StablePrefix            json.RawMessage `json:"stable_prefix"`
		VisibleHistory          []Message       `json:"visible_history"`
		ProviderContinuationRef string          `json:"provider_continuation_ref,omitempty"`
	}{"executionharness.request.v1", spec.Identity, prefix, messages, continuation}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return NormalizedModelRequest{}, err
	}
	return NormalizedModelRequest{
		RunIdentity: spec.Identity, StablePrefix: prefix, VisibleHistory: messages,
		ProviderContinuationRef: continuation, CanonicalBytes: canonical, CanonicalDigest: sha256Bytes(canonical),
	}, nil
}

func validateHistory(runID string, history []Event, identityDigest string) error {
	for i, event := range history {
		if event.RunID != runID || event.Sequence != uint64(i+1) {
			return fmt.Errorf("%w: non-monotonic sequence or run binding", ErrHistoryCorrupt)
		}
		if i == 0 {
			if event.Type != EventRunStarted || event.IdentityDigest != identityDigest {
				return ErrRunIdentityDrift
			}
		}
	}
	return nil
}

func cloneToolRequests(input []ToolRequest) []ToolRequest {
	result := make([]ToolRequest, len(input))
	for i, request := range input {
		result[i] = request
		result[i].Arguments = append([]byte(nil), request.Arguments...)
	}
	return result
}
