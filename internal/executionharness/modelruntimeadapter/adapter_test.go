package modelruntimeadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type fakeCreator struct {
	commands []modelruntime.CreateInvocationCommand
	err      error
	drift    bool
	reused   *modelruntime.Invocation
	outcome  modelruntime.DispatchResult
	outcomes int
	found    *modelruntime.Invocation
	input    modelruntime.PreparedModelInput
	findErr  error
	findKeys []string
}

func (f *fakeCreator) Create(_ context.Context, command modelruntime.CreateInvocationCommand) (modelruntime.CreateInvocationResult, error) {
	f.commands = append(f.commands, command)
	if f.err != nil {
		return modelruntime.CreateInvocationResult{}, f.err
	}
	if f.reused != nil {
		return modelruntime.CreateInvocationResult{Invocation: *f.reused, Reused: true}, nil
	}
	id := int64(len(f.commands))
	assignmentID, principalID := int64(100+id), int64(200+id)
	invocation := modelruntime.Invocation{
		ID: id, OrganizationID: command.OrganizationID, TaskID: command.TaskID, AttemptID: command.AttemptID,
		SubjectRoleID: command.SubjectRoleID, ContextSnapshotID: command.ContextSnapshotID,
		CorrelationID: command.CorrelationID, CausationID: command.CausationID,
		DispatcherAssignmentID: &assignmentID, ExecutionPrincipalID: &principalID,
		OutputMode: command.OutputMode, OutputSchema: command.OutputSchema,
		MaxOutputTokens: command.MaxOutputTokens, Temperature: command.Temperature,
		ThinkingMode: command.ThinkingMode, RequiredCapabilities: command.RequiredCapabilities,
	}
	if f.drift {
		invocation.TaskID++
	}
	return modelruntime.CreateInvocationResult{Invocation: invocation}, nil
}

func (f *fakeCreator) Outcome(_ context.Context, _ int64) (modelruntime.DispatchResult, error) {
	f.outcomes++
	return f.outcome, nil
}

func (f *fakeCreator) FindIdempotent(_ context.Context, key string) (modelruntime.Invocation, modelruntime.PreparedModelInput, error) {
	f.findKeys = append(f.findKeys, key)
	if f.findErr != nil {
		return modelruntime.Invocation{}, modelruntime.PreparedModelInput{}, f.findErr
	}
	if f.found == nil {
		return modelruntime.Invocation{}, modelruntime.PreparedModelInput{}, modelruntime.ErrNotFound
	}
	return *f.found, f.input, nil
}

type fakeDispatcher struct {
	results []modelruntime.DispatchResult
	errAt   int
	calls   []int64
}

func (f *fakeDispatcher) Dispatch(_ context.Context, id int64) (modelruntime.DispatchResult, error) {
	f.calls = append(f.calls, id)
	if f.errAt == len(f.calls) {
		return modelruntime.DispatchResult{}, errors.New("dispatch denied by model runtime")
	}
	return f.results[len(f.calls)-1], nil
}

type allowAuthority struct{ calls int }

func (a *allowAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	a.calls++
	return nil
}

type toolCatalog struct {
	definition executionharness.ToolDefinition
}

func (c toolCatalog) Lookup(_ context.Context, name string) (executionharness.ToolDefinition, bool) {
	return c.definition, name == c.definition.Name
}
func (toolCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return nil
}

type toolExecutor struct{ calls int }

func (t *toolExecutor) Execute(_ context.Context, _ executionharness.RunIdentity, request executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	t.calls++
	return executionharness.ToolExecutionResult{Content: json.RawMessage(`{"value":"fixture-result"}`), Provenance: "fixture/v1"}, nil
}

func TestHarnessModelToolModelUsesOneModelRuntimeInvocationPerTurn(t *testing.T) {
	spec := fixtureSpec()
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{
		successfulDispatch(1, "", []modelruntime.ToolIntent{{ID: "call-1", Name: "lookup_fixture", Arguments: json.RawMessage(`{"key":"alpha"}`)}}),
		successfulDispatch(2, "fixture answer", nil),
	}}
	adapter := newAdapter(t, creator, dispatcher)
	authority := &allowAuthority{}
	tools := &toolExecutor{}
	history := executionharness.NewMemoryHistoryStore()
	runtime, err := executionharness.New(authority, adapter, toolCatalog{definition: spec.Tools[0]}, tools, history)
	if err != nil {
		t.Fatal(err)
	}

	result := runtime.Execute(context.Background(), spec)
	if result.Status != executionharness.StatusCompleted || result.FinalOutput != "fixture answer" {
		t.Fatalf("unexpected harness result: %+v", result)
	}
	if len(creator.commands) != 2 || len(dispatcher.calls) != 2 || tools.calls != 1 {
		t.Fatalf("turn boundary mismatch: create=%d dispatch=%d tools=%d", len(creator.commands), len(dispatcher.calls), tools.calls)
	}
	first, second := creator.commands[0], creator.commands[1]
	if first.OrganizationID != spec.Identity.OrganizationID || first.TaskID != spec.Identity.TaskID || first.AttemptID != spec.Identity.AttemptID ||
		first.SubjectRoleID != spec.Identity.RoleID || first.ContextSnapshotID != 41 || first.CorrelationID != spec.Identity.CorrelationID || first.CausationID != spec.Identity.CausationID {
		t.Fatalf("workflow binding was not preserved: %+v", first)
	}
	if first.ModelInput == nil || second.ModelInput == nil || first.ModelInput.CanonicalProjectionDigest == second.ModelInput.CanonicalProjectionDigest {
		t.Fatal("turn envelopes were not separately bound to their harness projections")
	}
	if !reflect.DeepEqual(first.ModelInput.StablePrefix, second.ModelInput.StablePrefix) || !reflect.DeepEqual(first.ModelInput.ToolDefinitions, second.ModelInput.ToolDefinitions) {
		t.Fatal("stable context/tool prefix drifted between turns")
	}
	if len(second.ModelInput.VisibleHistory) != 2 || second.ModelInput.VisibleHistory[0].Role != modelruntime.ModelInputRoleAssistant || second.ModelInput.VisibleHistory[1].Role != modelruntime.ModelInputRoleTool {
		t.Fatalf("second invocation lost the model/tool trajectory: %+v", second.ModelInput.VisibleHistory)
	}
	if second.ModelInput.VisibleHistory[1].Content != `{"value":"fixture-result"}` {
		t.Fatalf("tool result was not preserved byte-exactly: %q", second.ModelInput.VisibleHistory[1].Content)
	}
	if first.IdempotencyKey == second.IdempotencyKey || first.Deadline != second.Deadline {
		t.Fatalf("unexpected per-turn idempotency/deadline: first=%+v second=%+v", first, second)
	}
}

func TestDirectCompletionMapsUsageWithoutFabricatingReasoning(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	hit := int64(7)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatchWithUsage(1, "done", nil, modelruntime.Usage{InvocationID: 1, InputTokens: 30, OutputTokens: 4, PromptCacheHitTokens: &hit})}}
	adapter := newAdapter(t, creator, dispatcher)

	result, err := adapter.Invoke(context.Background(), spec.Identity, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinishReason != executionharness.FinishFinal || result.FinalOutput != "done" || result.InvocationRef != "1" ||
		result.Usage.InputTokens == nil || *result.Usage.InputTokens != 30 || result.Usage.CachedInputTokens == nil || *result.Usage.CachedInputTokens != 7 {
		t.Fatalf("unexpected result mapping: %+v", result)
	}
	if result.ReasoningTelemetry != (executionharness.ReasoningTelemetry{}) {
		t.Fatalf("adapter fabricated unavailable reasoning telemetry: %+v", result.ReasoningTelemetry)
	}
}

func TestProjectionAndBindingDriftFailBeforeInvocationCreation(t *testing.T) {
	spec := fixtureSpec()
	base := project(t, spec, nil)
	tests := []struct {
		name     string
		identity executionharness.RunIdentity
		mutate   func(*executionharness.NormalizedModelRequest)
	}{
		{"call identity", func() executionharness.RunIdentity { value := spec.Identity; value.TaskID++; return value }(), func(*executionharness.NormalizedModelRequest) {}},
		{"canonical bytes", spec.Identity, func(request *executionharness.NormalizedModelRequest) {
			request.CanonicalBytes = append(request.CanonicalBytes, ' ')
		}},
		{"detached stable prefix", spec.Identity, func(request *executionharness.NormalizedModelRequest) { request.StablePrefix = json.RawMessage(`{}`) }},
		{"detached visible history", spec.Identity, func(request *executionharness.NormalizedModelRequest) {
			request.VisibleHistory = []executionharness.Message{{Role: "assistant", Content: "injected"}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := cloneRequest(base)
			tc.mutate(&request)
			creator := &fakeCreator{}
			dispatcher := &fakeDispatcher{}
			adapter := newAdapter(t, creator, dispatcher)
			if _, err := adapter.Invoke(context.Background(), tc.identity, request); err == nil {
				t.Fatal("drift was accepted")
			}
			if len(creator.commands) != 0 || len(dispatcher.calls) != 0 {
				t.Fatalf("drift caused side effects: create=%d dispatch=%d", len(creator.commands), len(dispatcher.calls))
			}
		})
	}
}

func TestInvalidContextSnapshotReferenceFailsBeforeCreation(t *testing.T) {
	spec := fixtureSpec()
	spec.Context.ID = "context-not-an-id"
	request := project(t, spec, nil)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); !errors.Is(err, ErrInvalidProjection) {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(creator.commands) != 0 || len(dispatcher.calls) != 0 {
		t.Fatal("invalid context reference crossed the model runtime boundary")
	}
}

func TestModelRuntimeFailuresAndReturnedBindingDriftFailClosed(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	t.Run("create failure", func(t *testing.T) {
		creator := &fakeCreator{err: modelruntime.ErrEgressDenied}
		dispatcher := &fakeDispatcher{}
		adapter := newAdapter(t, creator, dispatcher)
		if _, err := adapter.Invoke(context.Background(), spec.Identity, request); !errors.Is(err, modelruntime.ErrEgressDenied) {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dispatcher.calls) != 0 {
			t.Fatal("dispatch occurred after invocation admission denial")
		}
	})
	t.Run("created binding drift", func(t *testing.T) {
		creator := &fakeCreator{drift: true}
		dispatcher := &fakeDispatcher{}
		adapter := newAdapter(t, creator, dispatcher)
		if _, err := adapter.Invoke(context.Background(), spec.Identity, request); !errors.Is(err, ErrBindingDrift) {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dispatcher.calls) != 0 {
			t.Fatal("dispatch occurred after durable invocation binding drift")
		}
	})
	t.Run("dispatch failure", func(t *testing.T) {
		creator := &fakeCreator{}
		dispatcher := &fakeDispatcher{errAt: 1}
		adapter := newAdapter(t, creator, dispatcher)
		if _, err := adapter.Invoke(context.Background(), spec.Identity, request); err == nil {
			t.Fatal("dispatch failure was hidden")
		}
		if len(creator.commands) != 1 || len(dispatcher.calls) != 1 {
			t.Fatal("model runtime service path was not followed")
		}
	})
}

func TestToolDenialIsProjectedAsDeterministicModelVisibleError(t *testing.T) {
	spec := fixtureSpec()
	request0 := project(t, spec, nil)
	toolRequest := executionharness.ToolRequest{ToolCallID: "call-1", ToolName: "lookup_fixture", Arguments: json.RawMessage(`{"key":"alpha"}`)}
	history := []executionharness.Event{
		{RunID: spec.Identity.RunID, Sequence: 1, Type: executionharness.EventRunStarted, IdentityDigest: identityDigestFromStarted(t, spec)},
		{RunID: spec.Identity.RunID, Sequence: 2, Type: executionharness.EventModelRequestPrepared, RequestDigest: request0.CanonicalDigest},
		{RunID: spec.Identity.RunID, Sequence: 3, Type: executionharness.EventModelResponseRecorded, ModelResult: &executionharness.ModelResult{FinishReason: executionharness.FinishTools, ToolRequests: []executionharness.ToolRequest{toolRequest}}},
		{RunID: spec.Identity.RunID, Sequence: 4, Type: executionharness.EventToolCallDenied, ToolRequest: &toolRequest, ErrorCode: "tool_not_allowed"},
	}
	request := project(t, spec, history)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatal(err)
	}
	visible := creator.commands[0].ModelInput.VisibleHistory
	if len(visible) != 2 || visible[1].Content != `{"error_code":"tool_not_allowed"}` {
		t.Fatalf("tool denial was not projected deterministically: %+v", visible)
	}
}

func TestToolIntentWithoutStableCallIDIsRejected(t *testing.T) {
	spec := fixtureSpec()
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "", []modelruntime.ToolIntent{{Name: "lookup_fixture", Arguments: json.RawMessage(`{}`)}})}}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, project(t, spec, nil)); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSucceededInvocationReuseReadsDurableOutcomeWithoutRedispatch(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	durable := successfulDispatch(7, "recovered answer", nil)
	invocation := durable.Invocation
	creator := &fakeCreator{found: &invocation, input: storedInput(request, 41), outcome: durable}
	dispatcher := &fakeDispatcher{}
	adapter := newAdapter(t, creator, dispatcher)
	result, err := adapter.Invoke(context.Background(), spec.Identity, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalOutput != "recovered answer" || result.InvocationRef != "7" || creator.outcomes != 1 || len(dispatcher.calls) != 0 || len(creator.commands) != 0 {
		t.Fatalf("recovery redispatched or lost outcome: result=%+v outcomes=%d dispatch=%d", result, creator.outcomes, len(dispatcher.calls))
	}
}

func TestNonterminalInvocationReuseFailsWithoutRedispatch(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	invocation := successfulDispatch(7, "", nil).Invocation
	invocation.Status = modelruntime.InvocationClaimed
	creator := &fakeCreator{found: &invocation, input: storedInput(request, 41)}
	dispatcher := &fakeDispatcher{}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unexpected error: %v", err)
	}
	if creator.outcomes != 0 || len(dispatcher.calls) != 0 {
		t.Fatal("nonterminal reused invocation caused a duplicate side effect")
	}
}

func TestExistingProjectionDriftFailsWithoutDispatch(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	invocation := successfulDispatch(7, "", nil).Invocation
	input := storedInput(request, 41)
	input.Envelope.CanonicalProjectionDigest = digest([]byte("other projection"))
	creator := &fakeCreator{found: &invocation, input: input}
	dispatcher := &fakeDispatcher{}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); !errors.Is(err, ErrBindingDrift) {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(creator.commands) != 0 || creator.outcomes != 0 || len(dispatcher.calls) != 0 {
		t.Fatal("drifted durable input caused a side effect")
	}
}

func fixtureSpec() executionharness.RunSpec {
	content := "authorized context fixture"
	return executionharness.RunSpec{
		Identity:   executionharness.RunIdentity{RunID: "run-1", OrganizationID: "explorarte", TaskID: 11, AttemptID: 22, RoleID: "research/worker", ExecutionPrincipalID: "principal-1", CorrelationID: "corr-1", CausationID: "cause-1"},
		LeaseToken: "lease-token",
		Context:    executionharness.InitialContext{ID: "41", Version: "v1", Digest: digest([]byte(content)), Content: content},
		Tools:      []executionharness.ToolDefinition{{Name: "lookup_fixture", Description: "read deterministic fixture", InputSchema: json.RawMessage(`{"type":"object","required":["key"],"properties":{"key":{"type":"string"}}}`)}},
		Policy:     executionharness.RunPolicy{MaxTurns: 3, MaxToolCalls: 2, ExecutionProfileID: "standard-v1", ModelPolicyRef: "canonical-role-binding", BuildRef: "build-1"},
	}
}

func newAdapter(t *testing.T, creator InvocationCreator, dispatcher InvocationDispatcher) *Adapter {
	t.Helper()
	value, err := New(creator, dispatcher, ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }), Config{MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, InvocationTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func project(t *testing.T, spec executionharness.RunSpec, history []executionharness.Event) executionharness.NormalizedModelRequest {
	t.Helper()
	request, err := executionharness.Project(spec, history)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func successfulDispatch(id int64, text string, intents []modelruntime.ToolIntent) modelruntime.DispatchResult {
	return successfulDispatchWithUsage(id, text, intents, modelruntime.Usage{InvocationID: id, InputTokens: 10, OutputTokens: 2})
}

func successfulDispatchWithUsage(id int64, text string, intents []modelruntime.ToolIntent, usage modelruntime.Usage) modelruntime.DispatchResult {
	assignmentID, principalID := int64(100+id), int64(200+id)
	invocation := modelruntime.Invocation{ID: id, OrganizationID: "explorarte", TaskID: 11, AttemptID: 22, SubjectRoleID: "research/worker", ContextSnapshotID: 41, CorrelationID: "corr-1", CausationID: "cause-1", DispatcherAssignmentID: &assignmentID, ExecutionPrincipalID: &principalID, Status: modelruntime.InvocationSucceeded, OutputMode: modelruntime.OutputText, MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled}
	return modelruntime.DispatchResult{Invocation: invocation, Result: &modelruntime.InvocationResult{InvocationID: id, OutputMode: modelruntime.OutputText, TextOutput: text, ToolIntents: intents}, Usage: &usage}
}

func storedInput(request executionharness.NormalizedModelRequest, contextSnapshotID int64) modelruntime.PreparedModelInput {
	body := []byte(`{"stored":"fixture"}`)
	return modelruntime.PreparedModelInput{
		Envelope: modelruntime.ModelInputEnvelope{
			SchemaVersion: modelruntime.ModelInputEnvelopeSchemaV1, ContextSnapshotID: contextSnapshotID,
			CanonicalProjectionDigest: request.CanonicalDigest,
		},
		CanonicalBytes: body, CanonicalDigest: digest(body),
	}
}

func cloneRequest(input executionharness.NormalizedModelRequest) executionharness.NormalizedModelRequest {
	output := input
	output.CanonicalBytes = append([]byte(nil), input.CanonicalBytes...)
	output.StablePrefix = append([]byte(nil), input.StablePrefix...)
	body, _ := json.Marshal(input.VisibleHistory)
	_ = json.Unmarshal(body, &output.VisibleHistory)
	return output
}

func identityDigestFromStarted(t *testing.T, spec executionharness.RunSpec) string {
	t.Helper()
	store := executionharness.NewMemoryHistoryStore()
	runtime, err := executionharness.New(&allowAuthority{}, &unusedModel{}, toolCatalog{definition: spec.Tools[0]}, &toolExecutor{}, store)
	if err != nil {
		t.Fatal(err)
	}
	_ = runtime.Execute(context.Background(), spec)
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil || len(events) == 0 {
		t.Fatalf("derive run identity event: %v", err)
	}
	return events[0].IdentityDigest
}

type unusedModel struct{}

func (*unusedModel) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	return executionharness.ModelResult{}, errors.New("stop after run start")
}

func TestCanonicalBytesPassedThroughDigestBinding(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatal(err)
	}
	if got := creator.commands[0].ModelInput.CanonicalProjectionDigest; got != request.CanonicalDigest {
		t.Fatalf("projection digest not bound: got=%s want=%s", got, request.CanonicalDigest)
	}
	if !bytes.Equal([]byte(creator.commands[0].ModelInput.StablePrefix[0].Content), []byte(spec.Context.Content)) {
		t.Fatal("context bytes drifted")
	}
}

// TestExecutionContractReachesModelInputStablePrefix proves that execution-time
// contract guidance is delivered to the model as a separate stable-prefix user
// message AFTER the byte-exact context render, without modifying that render.
// This is the boundary that lets a task whose durable context predates the
// contract still receive it at execution time.
func TestExecutionContractReachesModelInputStablePrefix(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}
	const contract = "task_class MUST use lowercase dotted syntax and never equal legacy.unspecified"
	adapter, err := New(creator, dispatcher, ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }), Config{
		MaxOutputTokens:               256,
		ThinkingMode:                  modelruntime.ThinkingDisabled,
		InvocationTTL:                 time.Hour,
		ExecutionContractInstructions: contract,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(creator.commands) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(creator.commands))
	}
	input := creator.commands[0].ModelInput
	if input == nil {
		t.Fatal("invocation has no model input")
	}
	prefix := input.StablePrefix
	if len(prefix) != 2 {
		t.Fatalf("expected 2 stable-prefix messages (context render + contract), got %d", len(prefix))
	}
	// First message stays the byte-exact context render (provenance intact).
	if prefix[0].Role != modelruntime.ModelInputRoleUser || prefix[0].Content != spec.Context.Content {
		t.Fatalf("stable prefix[0] must be the exact context render, got role=%q content=%q", prefix[0].Role, prefix[0].Content)
	}
	// Second message carries the contract.
	if prefix[1].Role != modelruntime.ModelInputRoleUser || prefix[1].Content != contract {
		t.Fatalf("stable prefix[1] must carry the execution contract, got role=%q content=%q", prefix[1].Role, prefix[1].Content)
	}
	// Prove Model Runtime itself accepts the contract-augmented envelope: the
	// first prefix message still binds byte-exact to the snapshot render.
	snapshot := modelruntime.ContextSnapshotRef{ID: 41, RenderedHash: digest([]byte(spec.Context.Content))}
	if _, err := modelruntime.PrepareModelInput(input, snapshot, []byte(spec.Context.Content)); err != nil {
		t.Fatalf("Model Runtime rejected the contract-augmented envelope: %v", err)
	}
}

// TestNoExecutionContractKeepsSingleMessagePrefix proves legacy behavior is
// preserved when no execution contract is configured.
func TestNoExecutionContractKeepsSingleMessagePrefix(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}
	adapter := newAdapter(t, creator, dispatcher)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	input := creator.commands[0].ModelInput
	if len(input.StablePrefix) != 1 {
		t.Fatalf("expected 1 stable-prefix message without a contract, got %d", len(input.StablePrefix))
	}
}

// contractAdapter builds an adapter that carries execution-contract
// instructions, mirroring newAdapter for every other field.
func contractAdapter(t *testing.T, creator InvocationCreator, dispatcher InvocationDispatcher, contract string) *Adapter {
	t.Helper()
	value, err := New(creator, dispatcher, ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }), Config{
		MaxOutputTokens:               256,
		ThinkingMode:                  modelruntime.ThinkingDisabled,
		InvocationTTL:                 time.Hour,
		ExecutionContractInstructions: contract,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// TestSameExecutionContractProducesSameIdempotencyKey proves re-entry under
// the SAME execution contract is deterministic: the same projection, output
// contract and execution contract yield the same invocation idempotency key,
// so a resumed attempt looks up and adopts its existing invocation instead of
// creating a second one.
func TestSameExecutionContractProducesSameIdempotencyKey(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	const contract = "task_class MUST use lowercase dotted syntax and never equal legacy.unspecified"

	firstCreator := &fakeCreator{}
	if _, err := contractAdapter(t, firstCreator, &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}, contract).Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatal(err)
	}
	secondCreator := &fakeCreator{}
	if _, err := contractAdapter(t, secondCreator, &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}, contract).Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatal(err)
	}
	if len(firstCreator.commands) != 1 || len(secondCreator.commands) != 1 {
		t.Fatalf("expected one invocation per adapter, got %d and %d", len(firstCreator.commands), len(secondCreator.commands))
	}
	first := firstCreator.commands[0].IdempotencyKey
	second := secondCreator.commands[0].IdempotencyKey
	if first == "" || first != second {
		t.Fatalf("same contract must produce the same idempotency key: %q vs %q", first, second)
	}

	// A re-entry that finds the durable invocation under that key adopts it
	// without creating or dispatching a second one, and the lookup itself
	// used exactly the key the original creation used.
	durable := successfulDispatch(7, "recovered answer", nil)
	invocation := durable.Invocation
	reuseCreator := &fakeCreator{found: &invocation, input: storedInput(request, 41), outcome: durable}
	reuseDispatcher := &fakeDispatcher{}
	result, err := contractAdapter(t, reuseCreator, reuseDispatcher, contract).Invoke(context.Background(), spec.Identity, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.InvocationRef != "7" || len(reuseCreator.commands) != 0 || len(reuseDispatcher.calls) != 0 {
		t.Fatalf("same-contract re-entry must adopt the existing invocation: ref=%s created=%d dispatched=%d",
			result.InvocationRef, len(reuseCreator.commands), len(reuseDispatcher.calls))
	}
	if len(reuseCreator.findKeys) != 1 || reuseCreator.findKeys[0] != first {
		t.Fatalf("re-entry looked up key %v, want the original creation key %q", reuseCreator.findKeys, first)
	}
}

// TestDifferentExecutionContractProducesDifferentIdempotencyKey proves a
// re-entry under DIFFERENT execution-contract instructions cannot adopt an
// invocation created under other instructions: the key changes with the
// contract, exactly like the output schema it sits beside. A run without a
// contract keeps the historical key shape.
func TestDifferentExecutionContractProducesDifferentIdempotencyKey(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)

	invokeWithContract := func(contract string) string {
		t.Helper()
		creator := &fakeCreator{}
		if _, err := contractAdapter(t, creator, &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}, contract).Invoke(context.Background(), spec.Identity, request); err != nil {
			t.Fatal(err)
		}
		if len(creator.commands) != 1 {
			t.Fatalf("expected one invocation, got %d", len(creator.commands))
		}
		return creator.commands[0].IdempotencyKey
	}

	keyA := invokeWithContract("contract A")
	keyB := invokeWithContract("contract B")
	keyNone := invokeWithContract("")

	if keyA == keyB {
		t.Fatalf("different execution contracts must produce different idempotency keys: %q", keyA)
	}
	if keyA == keyNone || keyB == keyNone {
		t.Fatalf("a contract run must not share the no-contract key: A=%q B=%q none=%q", keyA, keyB, keyNone)
	}
	// The no-contract key keeps the historical shape: projection digest +
	// output contract, with no extra component appended.
	prefix := "execution-harness:" + request.CanonicalDigest + ":"
	rest := strings.TrimPrefix(keyNone, prefix)
	if !strings.HasPrefix(keyNone, prefix) || rest == "" || strings.Contains(rest, ":") {
		t.Fatalf("no-contract key changed shape: %q", keyNone)
	}
}
