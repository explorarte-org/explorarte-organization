package executionharness

import (
	"context"
	"errors"
	"fmt"
)

type Runtime struct {
	authority ExecutionAuthorityPort
	models    ModelExecutor
	catalog   ToolCatalog
	tools     ToolExecutor
	history   ExecutionHistoryStore
}

func New(authority ExecutionAuthorityPort, models ModelExecutor, catalog ToolCatalog, tools ToolExecutor, history ExecutionHistoryStore) (*Runtime, error) {
	if authority == nil || models == nil || catalog == nil || tools == nil || history == nil {
		return nil, fmt.Errorf("%w: harness dependencies are incomplete", ErrInvalidRun)
	}
	return &Runtime{authority: authority, models: models, catalog: catalog, tools: tools, history: history}, nil
}

func (r *Runtime) Execute(ctx context.Context, spec RunSpec) RunResult {
	spec = cloneRunSpec(spec)
	if err := ctx.Err(); err != nil {
		return RunResult{RunID: spec.Identity.RunID, Status: StatusCancelled, TerminationReason: "execution context cancelled", Provenance: "executionharness/v1"}
	}
	_, identityDigest, err := validateSpec(spec)
	if err != nil {
		return RunResult{RunID: spec.Identity.RunID, Status: StatusIdentityDrift, TerminationReason: err.Error(), Provenance: "executionharness/v1"}
	}
	events, err := r.history.Read(ctx, spec.Identity.RunID)
	if err != nil {
		return historyFailure(spec, nil, err)
	}
	if len(events) == 0 {
		events, err = r.append(ctx, spec, events, Event{Type: EventRunStarted, IdentityDigest: identityDigest})
		if err != nil {
			return historyFailure(spec, events, err)
		}
	} else if err = validateHistory(spec.Identity.RunID, events, identityDigest); err != nil {
		return result(spec, events, StatusIdentityDrift, "stable run identity, context, tool set, or policy changed", "", "", countTurns(events), countToolCalls(events))
	}
	if terminal, found, terminalErr := terminalResult(spec, events); terminalErr != nil {
		return historyFailure(spec, events, terminalErr)
	} else if found {
		return terminal
	}

	turnsUsed := countTurns(events)
	toolCallsUsed := countToolCalls(events)
	seenCalls := requestedToolCallIDs(events)
	lastModelOutput := lastModelOutput(events)

	for {
		if err := ctx.Err(); err != nil {
			events, _ = r.append(context.WithoutCancel(ctx), spec, events, Event{Type: EventRunCancelled, TerminalStatus: StatusCancelled, ErrorCode: "context_cancelled", Reason: "execution context cancelled"})
			return result(spec, events, StatusCancelled, "execution context cancelled", "", lastModelOutput, turnsUsed, toolCallsUsed)
		}
		if turnsUsed >= spec.Policy.MaxTurns {
			events, _ = r.append(ctx, spec, events, Event{Type: EventRunLimitReached, TerminalStatus: StatusLimitReached, ErrorCode: "max_turns", Reason: "max turns exhausted"})
			return result(spec, events, StatusLimitReached, "max turns exhausted", "", lastModelOutput, turnsUsed, toolCallsUsed)
		}
		if err := r.authority.AuthorizeExecution(ctx, AuthorityRequest{Identity: spec.Identity, LeaseToken: spec.LeaseToken}); err != nil {
			events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusAuthorizationDenied, ErrorCode: "authorization_denied", Reason: "execution authority denied"})
			return result(spec, events, StatusAuthorizationDenied, "execution authority denied", "", lastModelOutput, turnsUsed, toolCallsUsed)
		}

		request, err := Project(spec, events)
		if err != nil {
			return result(spec, events, StatusIdentityDrift, err.Error(), "", lastModelOutput, turnsUsed, toolCallsUsed)
		}
		events, err = r.append(ctx, spec, events, Event{Type: EventModelRequestPrepared, RequestDigest: request.CanonicalDigest})
		if err != nil {
			return historyFailure(spec, events, err)
		}
		modelResult, invokeErr := r.models.Invoke(ctx, spec.Identity, request)
		turnsUsed++
		if invokeErr != nil {
			events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusModelError, ErrorCode: "model_error", Reason: "model execution failed"})
			return result(spec, events, StatusModelError, "model execution failed", "", lastModelOutput, turnsUsed, toolCallsUsed)
		}
		modelResult.ToolRequests = cloneToolRequests(modelResult.ToolRequests)
		invalidModelArguments := false
		for index := range modelResult.ToolRequests {
			canonical, canonicalErr := canonicalJSON(modelResult.ToolRequests[index].Arguments)
			if canonicalErr != nil {
				invalidModelArguments = true
				break
			}
			modelResult.ToolRequests[index].Arguments = canonical
		}
		events, err = r.append(ctx, spec, events, Event{Type: EventModelResponseRecorded, ModelResult: &modelResult})
		if err != nil {
			return historyFailure(spec, events, err)
		}
		lastModelOutput = modelResult.FinalOutput
		if invalidModelArguments {
			events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusModelError, ErrorCode: "invalid_model_tool_arguments", Reason: "model returned non-canonical tool arguments"})
			return result(spec, events, StatusModelError, "model returned non-canonical tool arguments", "", lastModelOutput, turnsUsed, toolCallsUsed)
		}

		if modelResult.FinishReason == FinishFinal && len(modelResult.ToolRequests) == 0 {
			events, err = r.append(ctx, spec, events, Event{Type: EventRunCompleted, TerminalStatus: StatusCompleted, Reason: "final output"})
			if err != nil {
				return historyFailure(spec, events, err)
			}
			return result(spec, events, StatusCompleted, "final output", modelResult.FinalOutput, lastModelOutput, turnsUsed, toolCallsUsed)
		}
		if modelResult.FinishReason != FinishTools || len(modelResult.ToolRequests) == 0 {
			events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusModelError, ErrorCode: "invalid_model_result", Reason: "invalid normalized model result"})
			return result(spec, events, StatusModelError, "invalid normalized model result", "", lastModelOutput, turnsUsed, toolCallsUsed)
		}

		for _, toolRequest := range modelResult.ToolRequests {
			events, err = r.append(ctx, spec, events, Event{Type: EventToolCallRequested, ToolRequest: &toolRequest})
			if err != nil {
				return historyFailure(spec, events, err)
			}
			canonicalArgs, canonicalErr := canonicalJSON(toolRequest.Arguments)
			if canonicalErr == nil {
				toolRequest.Arguments = canonicalArgs
			}
			catalogDefinition, known := r.catalog.Lookup(ctx, toolRequest.ToolName)
			exposedDefinition, exposed := allowedTool(spec.Tools, toolRequest.ToolName)
			denialCode := ""
			switch {
			case !toolCallIDPattern.MatchString(toolRequest.ToolCallID):
				denialCode = "invalid_tool_call_id"
			case seenCalls[toolRequest.ToolCallID]:
				denialCode = "tool_call_replay"
			case !known:
				denialCode = "unknown_tool"
			case !exposed:
				denialCode = "tool_not_allowed"
			case !sameToolDefinition(catalogDefinition, exposedDefinition):
				denialCode = "tool_definition_drift"
			case canonicalErr != nil:
				denialCode = "invalid_tool_arguments"
			case toolCallsUsed >= spec.Policy.MaxToolCalls:
				denialCode = "max_tool_calls"
			}
			seenCalls[toolRequest.ToolCallID] = true
			if denialCode != "" {
				events, _ = r.append(ctx, spec, events, Event{Type: EventToolCallDenied, ToolRequest: &toolRequest, ErrorCode: denialCode})
				if denialCode == "max_tool_calls" {
					events, _ = r.append(ctx, spec, events, Event{Type: EventRunLimitReached, TerminalStatus: StatusLimitReached, ErrorCode: denialCode, Reason: "max tool calls exhausted"})
					return result(spec, events, StatusLimitReached, "max tool calls exhausted", "", lastModelOutput, turnsUsed, toolCallsUsed)
				}
				events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusToolError, ErrorCode: denialCode, Reason: denialCode})
				return result(spec, events, StatusToolError, denialCode, "", lastModelOutput, turnsUsed, toolCallsUsed)
			}
			if err := r.authority.AuthorizeExecution(ctx, AuthorityRequest{Identity: spec.Identity, LeaseToken: spec.LeaseToken}); err != nil {
				events, _ = r.append(ctx, spec, events, Event{Type: EventToolCallDenied, ToolRequest: &toolRequest, ErrorCode: "authorization_denied", Reason: err.Error()})
				events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusAuthorizationDenied, ErrorCode: "authorization_denied", Reason: "execution authority denied before tool"})
				return result(spec, events, StatusAuthorizationDenied, "execution authority denied before tool", "", lastModelOutput, turnsUsed, toolCallsUsed)
			}
			if err := r.catalog.ValidateArguments(ctx, catalogDefinition, toolRequest.Arguments); err != nil {
				events, _ = r.append(ctx, spec, events, Event{Type: EventToolCallDenied, ToolRequest: &toolRequest, ErrorCode: "invalid_tool_arguments", Reason: err.Error()})
				events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusToolError, ErrorCode: "invalid_tool_arguments", Reason: "invalid tool arguments"})
				return result(spec, events, StatusToolError, "invalid tool arguments", "", lastModelOutput, turnsUsed, toolCallsUsed)
			}
			toolResult, toolErr := r.tools.Execute(ctx, spec.Identity, toolRequest)
			toolCallsUsed++
			if toolErr != nil {
				events, _ = r.append(ctx, spec, events, Event{Type: EventToolResultRecorded, ToolRequest: &toolRequest, ErrorCode: "tool_error", Reason: toolErr.Error()})
				events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusToolError, ErrorCode: "tool_error", Reason: "tool execution failed"})
				return result(spec, events, StatusToolError, "tool execution failed", "", lastModelOutput, turnsUsed, toolCallsUsed)
			}
			canonicalResult, err := canonicalJSON(toolResult.Content)
			if err != nil {
				events, _ = r.append(ctx, spec, events, Event{Type: EventToolResultRecorded, ToolRequest: &toolRequest, ErrorCode: "invalid_tool_result", Reason: err.Error()})
				events, _ = r.append(ctx, spec, events, Event{Type: EventRunFailed, TerminalStatus: StatusToolError, ErrorCode: "invalid_tool_result", Reason: "invalid tool result"})
				return result(spec, events, StatusToolError, "invalid tool result", "", lastModelOutput, turnsUsed, toolCallsUsed)
			}
			events, err = r.append(ctx, spec, events, Event{Type: EventToolResultRecorded, ToolRequest: &toolRequest, ToolResult: canonicalResult, ToolProvenance: toolResult.Provenance})
			if err != nil {
				return historyFailure(spec, events, err)
			}
		}
	}
}

func cloneRunSpec(spec RunSpec) RunSpec {
	result := spec
	result.Tools = make([]ToolDefinition, len(spec.Tools))
	for index, tool := range spec.Tools {
		result.Tools[index] = tool
		result.Tools[index].InputSchema = append([]byte(nil), tool.InputSchema...)
	}
	return result
}

func terminalResult(spec RunSpec, events []Event) (RunResult, bool, error) {
	for index, event := range events {
		if !terminalEventType(event.Type) {
			continue
		}
		if index != len(events)-1 {
			return RunResult{}, false, fmt.Errorf("%w: event follows terminal run event", ErrHistoryCorrupt)
		}
		if event.Reason == "" || !terminalStatusMatches(event.Type, event.TerminalStatus) {
			return RunResult{}, false, fmt.Errorf("%w: terminal event lacks a valid status/reason", ErrHistoryCorrupt)
		}
		last := lastModelOutput(events)
		final := ""
		if event.TerminalStatus == StatusCompleted {
			final = last
		}
		return result(spec, events, event.TerminalStatus, event.Reason, final, last, countTurns(events), countToolCalls(events)), true, nil
	}
	return RunResult{}, false, nil
}

func terminalEventType(eventType EventType) bool {
	switch eventType {
	case EventRunCompleted, EventRunFailed, EventRunLimitReached, EventRunCancelled:
		return true
	default:
		return false
	}
}

func terminalStatusMatches(eventType EventType, status RunStatus) bool {
	switch eventType {
	case EventRunCompleted:
		return status == StatusCompleted
	case EventRunLimitReached:
		return status == StatusLimitReached
	case EventRunCancelled:
		return status == StatusCancelled
	case EventRunFailed:
		switch status {
		case StatusModelError, StatusToolError, StatusAuthorizationDenied, StatusIdentityDrift, StatusHistoryError:
			return true
		}
	}
	return false
}

func (r *Runtime) append(ctx context.Context, spec RunSpec, events []Event, event Event) ([]Event, error) {
	event.RunID = spec.Identity.RunID
	event.CorrelationID = spec.Identity.CorrelationID
	event.CausationID = spec.Identity.CausationID
	appended, err := r.history.Append(ctx, spec.Identity.RunID, uint64(len(events)), event)
	if err != nil {
		return events, err
	}
	return append(events, appended), nil
}

func allowedTool(tools []ToolDefinition, name string) (ToolDefinition, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolDefinition{}, false
}

func sameToolDefinition(left, right ToolDefinition) bool {
	leftNormalized, leftNormalizeErr := normalizeTools([]ToolDefinition{left})
	rightNormalized, rightNormalizeErr := normalizeTools([]ToolDefinition{right})
	if leftNormalizeErr != nil || rightNormalizeErr != nil {
		return false
	}
	leftBody, leftErr := canonicalJSON(leftNormalized[0].InputSchema)
	rightBody, rightErr := canonicalJSON(rightNormalized[0].InputSchema)
	return leftErr == nil && rightErr == nil && leftNormalized[0].Name == rightNormalized[0].Name &&
		leftNormalized[0].Description == rightNormalized[0].Description && string(leftBody) == string(rightBody)
}

func requestedToolCallIDs(events []Event) map[string]bool {
	result := make(map[string]bool)
	for _, event := range events {
		if event.Type == EventToolCallRequested && event.ToolRequest != nil {
			result[event.ToolRequest.ToolCallID] = true
		}
	}
	return result
}

func countTurns(events []Event) int {
	count := 0
	for _, event := range events {
		if event.Type == EventModelResponseRecorded {
			count++
		}
	}
	return count
}

func countToolCalls(events []Event) int {
	count := 0
	for _, event := range events {
		if event.Type == EventToolResultRecorded && event.ErrorCode != "tool_call_denied" {
			count++
		}
	}
	return count
}

func lastModelOutput(events []Event) string {
	last := ""
	for _, event := range events {
		if event.Type == EventModelResponseRecorded && event.ModelResult != nil {
			last = event.ModelResult.FinalOutput
		}
	}
	return last
}

func result(spec RunSpec, events []Event, status RunStatus, reason, final, last string, turns, tools int) RunResult {
	lastSequence := uint64(0)
	if len(events) > 0 {
		lastSequence = events[len(events)-1].Sequence
	}
	return RunResult{RunID: spec.Identity.RunID, Status: status, FinalOutput: final, LastModelOutput: last,
		LastSequence: lastSequence, TurnsUsed: turns, ToolCallsUsed: tools, TerminationReason: reason, Provenance: "executionharness/v1"}
}

func historyFailure(spec RunSpec, events []Event, err error) RunResult {
	if errors.Is(err, ErrRunIdentityDrift) {
		return result(spec, events, StatusIdentityDrift, err.Error(), "", lastModelOutput(events), countTurns(events), countToolCalls(events))
	}
	return result(spec, events, StatusHistoryError, err.Error(), "", lastModelOutput(events), countTurns(events), countToolCalls(events))
}
