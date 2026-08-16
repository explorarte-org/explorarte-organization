package executionharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type fakeAuthority struct {
	calls  int
	denyAt int
}

func (f *fakeAuthority) AuthorizeExecution(context.Context, AuthorityRequest) error {
	f.calls++
	if f.denyAt > 0 && f.calls >= f.denyAt {
		return ErrAuthorityDenied
	}
	return nil
}

type fakeModel struct {
	results  []ModelResult
	errors   map[int]error
	calls    int
	requests []NormalizedModelRequest
}

func (f *fakeModel) Invoke(_ context.Context, _ RunIdentity, request NormalizedModelRequest) (ModelResult, error) {
	f.calls++
	copyRequest := request
	copyRequest.CanonicalBytes = append([]byte(nil), request.CanonicalBytes...)
	copyRequest.StablePrefix = append([]byte(nil), request.StablePrefix...)
	copyRequest.VisibleHistory = append([]Message(nil), request.VisibleHistory...)
	f.requests = append(f.requests, copyRequest)
	if err := f.errors[f.calls]; err != nil {
		return ModelResult{}, err
	}
	if f.calls > len(f.results) {
		return ModelResult{}, errors.New("unexpected model call")
	}
	return f.results[f.calls-1], nil
}

type fakeCatalog struct{ definitions map[string]ToolDefinition }

func (f fakeCatalog) Lookup(_ context.Context, name string) (ToolDefinition, bool) {
	tool, ok := f.definitions[name]
	return tool, ok
}
func (fakeCatalog) ValidateArguments(_ context.Context, _ ToolDefinition, raw []byte) error {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if _, ok := value["key"]; !ok {
		return errors.New("key is required")
	}
	return nil
}

type fakeToolExecutor struct {
	calls int
	errAt int
}

func (f *fakeToolExecutor) Execute(_ context.Context, _ RunIdentity, request ToolRequest) (ToolExecutionResult, error) {
	f.calls++
	if f.errAt == f.calls {
		return ToolExecutionResult{}, errors.New("fixture tool failed")
	}
	return ToolExecutionResult{Content: json.RawMessage(fmt.Sprintf(`{"value":"result-%s"}`, request.ToolCallID)), Provenance: "fixture/v1"}, nil
}

func baseSpec() RunSpec {
	contextBody := "fixed immutable fixture"
	return RunSpec{
		Identity:   RunIdentity{RunID: "run-1", OrganizationID: "org-1", TaskID: 11, AttemptID: 22, RoleID: "research/worker", ExecutionPrincipalID: "principal-1", CorrelationID: "corr-1", CausationID: "cause-1"},
		LeaseToken: "lease-token",
		Context:    InitialContext{ID: "context-1", Version: "v1", Digest: sha256Bytes([]byte(contextBody)), Content: contextBody},
		Tools:      []ToolDefinition{{Name: "lookup_fixture", Description: "read deterministic fixture", InputSchema: json.RawMessage(`{"type":"object","required":["key"]}`)}},
		Policy:     RunPolicy{MaxTurns: 3, MaxToolCalls: 2, ExecutionProfileID: "standard-v1", ModelPolicyRef: "model-policy-1", BuildRef: "build-1"},
	}
}

func toolRequest(id, name string) ToolRequest {
	return ToolRequest{ToolCallID: id, ToolName: name, Arguments: json.RawMessage(`{"key":"alpha"}`)}
}

func newTestRuntime(t *testing.T, authority *fakeAuthority, model *fakeModel, tools *fakeToolExecutor, catalog fakeCatalog, store *MemoryHistoryStore) *Runtime {
	t.Helper()
	runtime, err := New(authority, model, catalog, tools, store)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestModelToolModelFlowAndProjectionStability(t *testing.T) {
	spec := baseSpec()
	authority := &fakeAuthority{}
	model := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}, InvocationRef: "inv-1", ReasoningTelemetry: ReasoningTelemetry{ExposureKind: "provider_exposed", ProviderExposedReasoningTrace: "telemetry only", Provenance: "provider/fixture"}},
		{FinishReason: FinishFinal, FinalOutput: "fixture answer", InvocationRef: "inv-2"},
	}}
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newTestRuntime(t, authority, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)

	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusCompleted || got.FinalOutput != "fixture answer" || model.calls != 2 || tools.calls != 1 {
		t.Fatalf("unexpected result=%+v model_calls=%d tool_calls=%d", got, model.calls, tools.calls)
	}
	if !bytes.Equal(model.requests[0].StablePrefix, model.requests[1].StablePrefix) {
		t.Fatal("stable context/tool prefix changed between turns")
	}
	if len(model.requests[1].VisibleHistory) != 2 || model.requests[1].VisibleHistory[0].Role != "assistant" || model.requests[1].VisibleHistory[1].Role != "tool" {
		t.Fatalf("second request does not preserve earlier visible trajectory: %+v", model.requests[1].VisibleHistory)
	}
	if bytes.Contains(model.requests[1].CanonicalBytes, []byte("telemetry only")) {
		t.Fatal("reasoning telemetry became model-visible history")
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) || event.CorrelationID != "corr-1" || event.CausationID != "cause-1" {
			t.Fatalf("invalid event identity/sequence: %+v", event)
		}
	}
	if !hasEvent(events, EventToolResultRecorded) || !hasEvent(events, EventRunCompleted) {
		t.Fatalf("missing causal events: %+v", events)
	}

	projectedA, err := Project(spec, events[:5])
	if err != nil {
		t.Fatal(err)
	}
	projectedB, err := Project(spec, events[:5])
	if err != nil {
		t.Fatal(err)
	}
	if projectedA.CanonicalDigest != projectedB.CanonicalDigest || !bytes.Equal(projectedA.CanonicalBytes, projectedB.CanonicalBytes) {
		t.Fatal("same inputs/history did not produce byte-identical canonical request")
	}
}

func TestDirectCompletion(t *testing.T) {
	spec := baseSpec()
	model := &fakeModel{results: []ModelResult{{FinishReason: FinishFinal, FinalOutput: "done", InvocationRef: "inv-1"}}}
	tools := &fakeToolExecutor{}
	runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, NewMemoryHistoryStore())
	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusCompleted || got.FinalOutput != "done" || model.calls != 1 || tools.calls != 0 {
		t.Fatalf("unexpected direct completion: %+v calls=%d/%d", got, model.calls, tools.calls)
	}
}

func TestTerminalRunsCannotBeReplayed(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		spec := baseSpec()
		model := &fakeModel{results: []ModelResult{{FinishReason: FinishFinal, FinalOutput: "done", InvocationRef: "inv-1"}}}
		tools := &fakeToolExecutor{}
		store := NewMemoryHistoryStore()
		runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
		first := runtime.Execute(context.Background(), spec)
		assertTerminalReplay(t, runtime, store, spec, first, model, tools)
	})

	t.Run("failed", func(t *testing.T) {
		spec := baseSpec()
		model := &fakeModel{}
		tools := &fakeToolExecutor{}
		store := NewMemoryHistoryStore()
		runtime := newTestRuntime(t, &fakeAuthority{denyAt: 1}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
		first := runtime.Execute(context.Background(), spec)
		assertTerminalReplay(t, runtime, store, spec, first, model, tools)
	})

	t.Run("limit reached", func(t *testing.T) {
		spec := baseSpec()
		spec.Policy.MaxTurns = 1
		model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}}}}
		tools := &fakeToolExecutor{}
		store := NewMemoryHistoryStore()
		runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
		first := runtime.Execute(context.Background(), spec)
		assertTerminalReplay(t, runtime, store, spec, first, model, tools)
	})

	t.Run("cancelled", func(t *testing.T) {
		spec := baseSpec()
		_, digest, err := validateSpec(spec)
		if err != nil {
			t.Fatal(err)
		}
		model := &fakeModel{}
		tools := &fakeToolExecutor{}
		store := NewMemoryHistoryStore()
		_, err = store.Append(context.Background(), spec.Identity.RunID, 0, Event{RunID: spec.Identity.RunID, Type: EventRunStarted, IdentityDigest: digest, CorrelationID: spec.Identity.CorrelationID, CausationID: spec.Identity.CausationID})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Append(context.Background(), spec.Identity.RunID, 1, Event{RunID: spec.Identity.RunID, Type: EventRunCancelled, TerminalStatus: StatusCancelled, ErrorCode: "context_cancelled", Reason: "execution context cancelled", CorrelationID: spec.Identity.CorrelationID, CausationID: spec.Identity.CausationID})
		if err != nil {
			t.Fatal(err)
		}
		runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
		first := runtime.Execute(context.Background(), spec)
		assertTerminalReplay(t, runtime, store, spec, first, model, tools)
	})
}

func assertTerminalReplay(t *testing.T, runtime *Runtime, store *MemoryHistoryStore, spec RunSpec, first RunResult, model *fakeModel, tools *fakeToolExecutor) {
	t.Helper()
	if first.Status != StatusCompleted && first.Status != StatusAuthorizationDenied && first.Status != StatusLimitReached && first.Status != StatusCancelled {
		t.Fatalf("first execution was not the expected terminal result: %+v", first)
	}
	before, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	modelCalls, toolCalls := model.calls, tools.calls
	second := runtime.Execute(context.Background(), spec)
	after, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("terminal replay changed result: first=%+v second=%+v", first, second)
	}
	if model.calls != modelCalls || tools.calls != toolCalls {
		t.Fatalf("terminal replay caused side effects: model delta=%d tool delta=%d", model.calls-modelCalls, tools.calls-toolCalls)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("terminal replay mutated history: before=%+v after=%+v", before, after)
	}
}

func TestAuthorityDenialHasZeroSideEffects(t *testing.T) {
	spec := baseSpec()
	authority := &fakeAuthority{denyAt: 1}
	model := &fakeModel{}
	tools := &fakeToolExecutor{}
	runtime := newTestRuntime(t, authority, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, NewMemoryHistoryStore())
	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusAuthorizationDenied || model.calls != 0 || tools.calls != 0 {
		t.Fatalf("deny did not fail before side effects: %+v calls=%d/%d", got, model.calls, tools.calls)
	}
}

func TestCancelledContextHasZeroSideEffects(t *testing.T) {
	spec := baseSpec()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	model := &fakeModel{}
	tools := &fakeToolExecutor{}
	runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, NewMemoryHistoryStore())
	got := runtime.Execute(ctx, spec)
	if got.Status != StatusCancelled || model.calls != 0 || tools.calls != 0 {
		t.Fatalf("cancelled run caused side effects: %+v calls=%d/%d", got, model.calls, tools.calls)
	}
}

func TestAuthorityRevokedBeforeNextTurn(t *testing.T) {
	spec := baseSpec()
	authority := &fakeAuthority{denyAt: 3} // model 1 allow, tool 1 allow, model 2 deny
	model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}}}}
	tools := &fakeToolExecutor{}
	runtime := newTestRuntime(t, authority, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, NewMemoryHistoryStore())
	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusAuthorizationDenied || model.calls != 1 || tools.calls != 1 {
		t.Fatalf("revocation allowed a later side effect: %+v calls=%d/%d", got, model.calls, tools.calls)
	}
}

func TestUnknownAndUnexposedToolsHaveZeroExecution(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request ToolRequest
		catalog map[string]ToolDefinition
		code    string
	}{
		{"unknown", toolRequest("call-1", "missing_tool"), map[string]ToolDefinition{"lookup_fixture": baseSpec().Tools[0]}, "unknown_tool"},
		{"known but unexposed", toolRequest("call-1", "other_fixture"), map[string]ToolDefinition{"lookup_fixture": baseSpec().Tools[0], "other_fixture": {Name: "other_fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}}, "tool_not_allowed"},
		{"definition drift", toolRequest("call-1", "lookup_fixture"), map[string]ToolDefinition{"lookup_fixture": {Name: "lookup_fixture", Description: "changed", InputSchema: json.RawMessage(`{"type":"object"}`)}}, "tool_definition_drift"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := baseSpec()
			model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{tc.request}}}}
			tools := &fakeToolExecutor{}
			store := NewMemoryHistoryStore()
			runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: tc.catalog}, store)
			got := runtime.Execute(context.Background(), spec)
			if got.Status != StatusToolError || tools.calls != 0 {
				t.Fatalf("invalid tool executed: %+v calls=%d", got, tools.calls)
			}
			events, _ := store.Read(context.Background(), spec.Identity.RunID)
			if !eventHasCode(events, EventToolCallDenied, tc.code) {
				t.Fatalf("missing denial %s: %+v", tc.code, events)
			}
		})
	}
}

func TestToolReplayExecutesOnlyOnce(t *testing.T) {
	spec := baseSpec()
	repeated := toolRequest("call-1", "lookup_fixture")
	model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{repeated, repeated}}}}
	tools := &fakeToolExecutor{}
	runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, NewMemoryHistoryStore())
	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusToolError || tools.calls != 1 || got.TerminationReason != "tool_call_replay" {
		t.Fatalf("replay not denied: %+v calls=%d", got, tools.calls)
	}
}

func TestMaxToolCallsAndTurnsPreserveHistory(t *testing.T) {
	t.Run("tool calls", func(t *testing.T) {
		spec := baseSpec()
		spec.Policy.MaxToolCalls = 1
		model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture"), toolRequest("call-2", "lookup_fixture")}}}}
		tools := &fakeToolExecutor{}
		store := NewMemoryHistoryStore()
		runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
		got := runtime.Execute(context.Background(), spec)
		if got.Status != StatusLimitReached || tools.calls != 1 {
			t.Fatalf("tool budget failed: %+v calls=%d", got, tools.calls)
		}
	})

	t.Run("turn after tool result", func(t *testing.T) {
		spec := baseSpec()
		spec.Policy.MaxTurns = 1
		model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}}}}
		tools := &fakeToolExecutor{}
		store := NewMemoryHistoryStore()
		runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
		got := runtime.Execute(context.Background(), spec)
		events, _ := store.Read(context.Background(), spec.Identity.RunID)
		if got.Status != StatusLimitReached || got.FinalOutput != "" || model.calls != 1 || tools.calls != 1 || !hasEvent(events, EventToolResultRecorded) {
			t.Fatalf("turn limit lost history or fabricated output: %+v calls=%d/%d events=%+v", got, model.calls, tools.calls, events)
		}
	})
}

func TestPriorHistorySurvivesModelAndToolErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		modelErr  map[int]error
		toolErrAt int
		want      RunStatus
	}{
		{"model error on second turn", map[int]error{2: errors.New("model unavailable")}, 0, StatusModelError},
		{"tool error", nil, 1, StatusToolError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := baseSpec()
			model := &fakeModel{results: []ModelResult{{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}}}, errors: tc.modelErr}
			tools := &fakeToolExecutor{errAt: tc.toolErrAt}
			store := NewMemoryHistoryStore()
			runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
			got := runtime.Execute(context.Background(), spec)
			events, _ := store.Read(context.Background(), spec.Identity.RunID)
			if got.Status != tc.want || !hasEvent(events, EventModelResponseRecorded) || !hasEvent(events, EventToolCallRequested) {
				t.Fatalf("prior causal evidence missing: %+v events=%+v", got, events)
			}
		})
	}
}

func TestStableRunDriftFailsClosed(t *testing.T) {
	original := baseSpec()
	_, digest, err := validateSpec(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*RunSpec){
		func(spec *RunSpec) { spec.Identity.RoleID = "other-role" },
		func(spec *RunSpec) {
			spec.Context.Content = "changed"
			spec.Context.Digest = sha256Bytes([]byte(spec.Context.Content))
		},
		func(spec *RunSpec) { spec.Tools[0].Description = "changed definition" },
		func(spec *RunSpec) { spec.Policy.MaxTurns++ },
	} {
		store := NewMemoryHistoryStore()
		_, err = store.Append(context.Background(), original.Identity.RunID, 0, Event{RunID: original.Identity.RunID, Type: EventRunStarted, IdentityDigest: digest, CorrelationID: original.Identity.CorrelationID, CausationID: original.Identity.CausationID})
		if err != nil {
			t.Fatal(err)
		}
		changed := original
		changed.Tools = append([]ToolDefinition(nil), original.Tools...)
		mutate(&changed)
		model := &fakeModel{}
		tools := &fakeToolExecutor{}
		runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": original.Tools[0]}}, store)
		got := runtime.Execute(context.Background(), changed)
		if got.Status != StatusIdentityDrift || model.calls != 0 || tools.calls != 0 {
			t.Fatalf("drift did not fail closed: %+v", got)
		}
	}
}

func TestHistoryStoreIsAppendOnlyAndReturnsDeepCopies(t *testing.T) {
	store := NewMemoryHistoryStore()
	event := Event{RunID: "run-1", Type: EventToolResultRecorded, ToolResult: json.RawMessage(`{"secret":"original"}`), ModelResult: &ModelResult{ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}}}
	appended, err := store.Append(context.Background(), "run-1", 0, event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Append(context.Background(), "run-1", 0, Event{RunID: "run-1", Type: EventRunFailed}); !errors.Is(err, ErrHistoryConflict) {
		t.Fatalf("stale expected sequence accepted: %v", err)
	}
	read, err := store.Read(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	read[0].ToolResult[0] = 'X'
	read[0].ModelResult.ToolRequests[0].Arguments[0] = 'X'
	again, _ := store.Read(context.Background(), "run-1")
	if !reflect.DeepEqual(again[0], appended) {
		t.Fatalf("caller mutation changed stored history: got=%+v want=%+v", again[0], appended)
	}
}

func TestEventAfterTerminalFailsClosed(t *testing.T) {
	spec := baseSpec()
	_, digest, err := validateSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryHistoryStore()
	events := []Event{
		{RunID: spec.Identity.RunID, Type: EventRunStarted, IdentityDigest: digest},
		{RunID: spec.Identity.RunID, Type: EventRunCompleted, TerminalStatus: StatusCompleted, Reason: "final output"},
		{RunID: spec.Identity.RunID, Type: EventModelRequestPrepared, RequestDigest: sha256Bytes([]byte("invalid continuation"))},
	}
	for index, event := range events {
		if _, err = store.Append(context.Background(), spec.Identity.RunID, uint64(index), event); err != nil {
			t.Fatal(err)
		}
	}
	model := &fakeModel{}
	tools := &fakeToolExecutor{}
	runtime := newTestRuntime(t, &fakeAuthority{}, model, tools, fakeCatalog{definitions: map[string]ToolDefinition{"lookup_fixture": spec.Tools[0]}}, store)
	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusHistoryError || model.calls != 0 || tools.calls != 0 {
		t.Fatalf("post-terminal history did not fail closed: %+v calls=%d/%d", got, model.calls, tools.calls)
	}
}

func hasEvent(events []Event, eventType EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func eventHasCode(events []Event, eventType EventType, code string) bool {
	for _, event := range events {
		if event.Type == eventType && event.ErrorCode == code {
			return true
		}
	}
	return false
}
