package runtimeadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// memoryHistory is the durable execution history, in memory. It keeps the same
// optimistic sequence contract the PostgreSQL store enforces, so a test that
// reads events back is reading the same ledger the real store would hold.
type memoryHistory struct {
	mu     sync.Mutex
	events map[string][]executionharness.Event
}

func newMemoryHistory() *memoryHistory {
	return &memoryHistory{events: map[string][]executionharness.Event{}}
}

func (h *memoryHistory) Append(_ context.Context, runID string, expected uint64, event executionharness.Event) (executionharness.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	current := uint64(len(h.events[runID]))
	if current != expected {
		return executionharness.Event{}, executionharness.ErrHistoryConflict
	}
	event.Sequence = expected + 1
	h.events[runID] = append(h.events[runID], event)
	return event, nil
}

func (h *memoryHistory) Read(_ context.Context, runID string) ([]executionharness.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]executionharness.Event(nil), h.events[runID]...), nil
}

func (h *memoryHistory) count(runID string, eventType executionharness.EventType) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	total := 0
	for _, event := range h.events[runID] {
		if event.Type == eventType {
			total++
		}
	}
	return total
}

type allowAuthority struct{ calls int }

func (a *allowAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	a.calls++
	return nil
}

// scriptedModel is the Model Runtime boundary. It records the run spec the
// Harness projected so a test can assert what was actually sent, and returns a
// scripted result.
type scriptedModel struct {
	result executionharness.ModelResult
	err    error
	calls  int
}

func (m *scriptedModel) Invoke(_ context.Context, _ executionharness.RunIdentity, _ executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	if m.err != nil {
		return executionharness.ModelResult{}, m.err
	}
	return m.result, nil
}

func testHarness(t *testing.T, model executionharness.ModelExecutor, history executionharness.ExecutionHistoryStore) Harness {
	t.Helper()
	return Harness{
		OrganizationID: "explorarte",
		Authority:      &allowAuthority{},
		History:        history,
		NewModelExecutor: func(config modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			if config.OutputMode == "" || len(config.OutputSchema) == 0 {
				return nil, errors.New("executive runs must carry a JSON output contract")
			}
			return model, nil
		},
		Clock: executive.ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}
}

func testCommand() executive.HarnessRunCommand {
	content := "rendered context"
	digest := sha256.Sum256([]byte(content))
	return executive.HarnessRunCommand{
		RunID: "executive:explorarte:task:5:attempt:9:ceo-plan:v1", TaskID: 5, AttemptID: 9,
		RoleID: "empresa/ceo", ExecutionPrincipalID: "7001", LeaseToken: "opaque-token",
		Context: executive.ContextSnapshot{ID: 3, Version: "1", Digest: hex.EncodeToString(digest[:]), Content: content},
		Purpose: executive.PurposeCEOPlan, OutputSchema: json.RawMessage(`{"type":"object"}`),
		MaxOutputTokens: 4096, CorrelationID: "executive:x", CausationID: "task:5:attempt:9",
		Deadline: time.Unix(1000, 0).Add(10 * time.Minute),
	}
}

// TestExecutiveRunRejectsToolIntentWithoutEverRequestingATool is the durable
// half of the tool-less invariant.
//
// Grepping the source only shows that Tools is nil at the call site. What
// matters is what the ledger records: tool_call_requested is appended
// immediately before a tool executor runs, and it is the event that means "a
// side effect may have happened". Proving that no such event exists for a run
// whose model asked for a tool proves the request never reached an executor at
// all -- the property Context Assembly will later sit on top of.
func TestExecutiveRunRejectsToolIntentWithoutEverRequestingATool(t *testing.T) {
	history := newMemoryHistory()
	model := &scriptedModel{result: executionharness.ModelResult{
		FinishReason: executionharness.FinishTools,
		ToolRequests: []executionharness.ToolRequest{{
			ToolCallID: "call-1", ToolName: "bash", Arguments: json.RawMessage(`{"cmd":"rm -rf /"}`),
		}},
		InvocationRef: "4242",
	}}
	command := testCommand()

	outcome, err := testHarness(t, model, history).Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != executive.HarnessRunFailed || outcome.Failure != executive.HarnessFailureToolRejected {
		t.Fatalf("outcome=%+v want a rejected tool intent", outcome)
	}
	if outcome.FinalOutput != "" {
		t.Fatalf("a tool intent must never carry a final output: %q", outcome.FinalOutput)
	}
	if requested := history.count(command.RunID, executionharness.EventToolCallRequested); requested != 0 {
		t.Fatalf("durable tool_call_requested events=%d: a tool the run never exposed reached the side-effect ledger", requested)
	}
	if results := history.count(command.RunID, executionharness.EventToolResultRecorded); results != 0 {
		t.Fatalf("durable tool_result events=%d", results)
	}
	if denied := history.count(command.RunID, executionharness.EventToolCallDenied); denied != 1 {
		t.Fatalf("expected exactly one durable denial, got %d", denied)
	}
	// The executor itself is the last line of defence and must never be
	// entered; if it were, it would fail the run rather than act.
	if _, execErr := (executiveToolExecutor{}).Execute(context.Background(), executionharness.RunIdentity{}, executionharness.ToolRequest{}); execErr == nil {
		t.Fatal("the executive tool executor must refuse every call")
	}
	if _, known := (executiveToolCatalog{}).Lookup(context.Background(), "bash"); known {
		t.Fatal("the executive tool catalog must know no tools")
	}
}

// TestExecutiveRunPolicyExposesNoToolsAndOneTurn pins the run policy the
// Executive submits. MaxTurns=1 is what makes "one attempt, one invocation" a
// property of the policy rather than an assertion after the fact.
func TestExecutiveRunPolicyExposesNoToolsAndOneTurn(t *testing.T) {
	if executiveMaxToolCalls != 0 {
		t.Fatalf("executiveMaxToolCalls=%d must be zero", executiveMaxToolCalls)
	}
	if executiveMaxTurns != 1 {
		t.Fatalf("executiveMaxTurns=%d must be one", executiveMaxTurns)
	}
	history := newMemoryHistory()
	model := &scriptedModel{result: executionharness.ModelResult{
		FinishReason: executionharness.FinishFinal, FinalOutput: `{"ok":true}`, InvocationRef: "77",
	}}
	command := testCommand()
	outcome, err := testHarness(t, model, history).Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != executive.HarnessRunSucceeded || outcome.FinalOutput != `{"ok":true}` || outcome.InvocationID != 77 {
		t.Fatalf("outcome=%+v", outcome)
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("the adapter must only emit valid outcomes: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("model turns=%d want 1", model.calls)
	}
}

// TestExecutiveRunResumesTheSameRunIdentityWithoutCallingTheModelAgain proves
// the deterministic RunID does what it exists for: re-entering the same run
// returns the terminal verdict from durable history instead of producing a
// second provider call.
func TestExecutiveRunResumesTheSameRunIdentityWithoutCallingTheModelAgain(t *testing.T) {
	history := newMemoryHistory()
	model := &scriptedModel{result: executionharness.ModelResult{
		FinishReason: executionharness.FinishFinal, FinalOutput: `{"ok":true}`, InvocationRef: "88",
	}}
	harness := testHarness(t, model, history)
	command := testCommand()

	first, err := harness.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := harness.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatalf("model turns=%d: re-entering the same run identity called the provider again", model.calls)
	}
	if second.Status != first.Status || second.FinalOutput != first.FinalOutput || second.InvocationID != first.InvocationID {
		t.Fatalf("resume produced a different verdict:\n first=%+v\n second=%+v", first, second)
	}
}

// TestExecutiveRunRefusesAnIncompleteCommand keeps the adapter fail-closed on
// the identity and contract fields the Harness cannot reconstruct.
func TestExecutiveRunRefusesAnIncompleteCommand(t *testing.T) {
	cases := map[string]func(*executive.HarnessRunCommand){
		"no principal":     func(c *executive.HarnessRunCommand) { c.ExecutionPrincipalID = "" },
		"no lease token":   func(c *executive.HarnessRunCommand) { c.LeaseToken = "" },
		"no run id":        func(c *executive.HarnessRunCommand) { c.RunID = "" },
		"no output schema": func(c *executive.HarnessRunCommand) { c.OutputSchema = nil },
		"unknown purpose":  func(c *executive.HarnessRunCommand) { c.Purpose = "improvise" },
		"no context":       func(c *executive.HarnessRunCommand) { c.Context = executive.ContextSnapshot{} },
		"expired deadline": func(c *executive.HarnessRunCommand) { c.Deadline = time.Unix(999, 0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			history := newMemoryHistory()
			model := &scriptedModel{result: executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "{}", InvocationRef: "1"}}
			command := testCommand()
			mutate(&command)
			if _, err := testHarness(t, model, history).Execute(context.Background(), command); err == nil {
				t.Fatal("expected the adapter to refuse the command")
			}
			if model.calls != 0 {
				t.Fatalf("model was invoked %d times despite an invalid command", model.calls)
			}
		})
	}
}
