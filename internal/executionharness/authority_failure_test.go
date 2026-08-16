package executionharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// errStoreDown stands in for whatever a real store reports when it cannot be
// reached. The point of the test is the classification, not the driver.
var errStoreDown = errors.New("fixture store unreachable")

// flakyAuthority fails at a chosen call with a chosen error and succeeds
// otherwise, so a test can place an outage at an exact boundary.
type flakyAuthority struct {
	calls   int
	failAt  int
	failErr error
	healed  bool
}

func (f *flakyAuthority) AuthorizeExecution(context.Context, AuthorityRequest) error {
	f.calls++
	if !f.healed && f.failAt > 0 && f.calls >= f.failAt {
		return f.failErr
	}
	return nil
}

// newRuntimeWith mirrors newTestRuntime but accepts any authority port, so a
// test can install a failure model the shared fake does not express.
func newRuntimeWith(t *testing.T, authority ExecutionAuthorityPort, model *fakeModel, tools *fakeToolExecutor, catalog fakeCatalog, store *MemoryHistoryStore) *Runtime {
	t.Helper()
	runtime, err := New(authority, model, catalog, tools, store)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func unavailable() error {
	return fmt.Errorf("%w: principal: %w", ErrAuthorityUnavailable, errStoreDown)
}

func toolThenFinal() *fakeModel {
	return &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}, InvocationRef: "inv-1"},
		{FinishReason: FinishFinal, FinalOutput: "final answer", InvocationRef: "inv-2"},
	}}
}

func catalogWithFixture() fakeCatalog {
	return fakeCatalog{definitions: map[string]ToolDefinition{
		"lookup_fixture": {Name: "lookup_fixture", Description: "read deterministic fixture", InputSchema: json.RawMessage(`{"type":"object","required":["key"]}`)},
	}}
}

// An outage at the very first authority check must not call the provider, must
// not be reported as a denial, and must not write a terminal event.
func TestAuthorityUnavailableIsNotADenialAndCallsNoProvider(t *testing.T) {
	authority := &flakyAuthority{failAt: 1, failErr: unavailable()}
	model := toolThenFinal()
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, authority, model, tools, catalogWithFixture(), store)

	got := runtime.Execute(context.Background(), baseSpec())

	if got.Status != StatusAuthorityUnavailable {
		t.Fatalf("status=%q want %q", got.Status, StatusAuthorityUnavailable)
	}
	if got.Status == StatusAuthorizationDenied {
		t.Fatal("an authority outage was reported as an authorization denial")
	}
	if !got.Retryable {
		t.Fatal("authority outage was not classified retryable")
	}
	if model.calls != 0 || tools.calls != 0 {
		t.Fatalf("side effects after loss of authority: provider=%d tool=%d", model.calls, tools.calls)
	}
	events, err := store.Read(context.Background(), baseSpec().Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if terminalEventType(event.Type) {
			t.Fatalf("outage wrote a terminal event %q, which would poison resume", event.Type)
		}
	}
}

// The decisive property: an outage between turns leaves the run resumable, and
// resuming does not repeat the model turn or the tool side effect.
func TestAuthorityUnavailableMidRunResumesWithoutRepeatingSideEffects(t *testing.T) {
	spec := baseSpec()
	// call 1 authorizes the first turn, call 2 authorizes the tool, call 3 is
	// the turn-2 boundary where the outage lands.
	authority := &flakyAuthority{failAt: 3, failErr: unavailable()}
	model := toolThenFinal()
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, authority, model, tools, catalogWithFixture(), store)

	interrupted := runtime.Execute(context.Background(), spec)
	if interrupted.Status != StatusAuthorityUnavailable || !interrupted.Retryable {
		t.Fatalf("interrupted=%+v", interrupted)
	}
	if model.calls != 1 || tools.calls != 1 {
		t.Fatalf("before resume: provider=%d tool=%d, want 1 and 1", model.calls, tools.calls)
	}

	authority.healed = true
	resumed := runtime.Execute(context.Background(), spec)

	if resumed.Status != StatusCompleted || resumed.FinalOutput != "final answer" {
		t.Fatalf("resumed=%+v", resumed)
	}
	if resumed.Retryable {
		t.Fatal("a completed run was reported retryable")
	}
	if model.calls != 2 {
		t.Fatalf("provider calls=%d, want exactly 2 across both attempts", model.calls)
	}
	if tools.calls != 1 {
		t.Fatalf("tool executed %d times, want exactly 1: the side effect was repeated", tools.calls)
	}
}

// An outage at the tool boundary leaves an unresolved tool_call_requested. The
// resumed run must be able to propose that same tool call id again: the model
// never saw it, so it is continuation and not replay.
func TestAuthorityUnavailableAtToolBoundaryDoesNotPoisonReplayGuard(t *testing.T) {
	spec := baseSpec()
	authority := &flakyAuthority{failAt: 2, failErr: unavailable()}
	model := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}, InvocationRef: "inv-1"},
		{FinishReason: FinishTools, ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}, InvocationRef: "inv-2"},
		{FinishReason: FinishFinal, FinalOutput: "final answer", InvocationRef: "inv-3"},
	}}
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, authority, model, tools, catalogWithFixture(), store)

	interrupted := runtime.Execute(context.Background(), spec)
	if interrupted.Status != StatusAuthorityUnavailable {
		t.Fatalf("interrupted=%+v", interrupted)
	}
	if tools.calls != 0 {
		t.Fatalf("tool ran %d times despite losing authority before execution", tools.calls)
	}

	authority.healed = true
	resumed := runtime.Execute(context.Background(), spec)
	if resumed.Status != StatusCompleted {
		t.Fatalf("resumed=%+v: an unresolved tool call was misread as a replay", resumed)
	}
	if tools.calls != 1 {
		t.Fatalf("tool executed %d times, want 1", tools.calls)
	}
}

// A real denial keeps the behaviour the previous slices proved: terminal,
// fail-closed, and explicitly not retryable.
func TestAuthorityDenialStaysTerminalAndNotRetryable(t *testing.T) {
	spec := baseSpec()
	authority := &flakyAuthority{failAt: 1, failErr: fmt.Errorf("%w: principal inactive or binding mismatch", ErrAuthorityDenied)}
	model := toolThenFinal()
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, authority, model, tools, catalogWithFixture(), store)

	denied := runtime.Execute(context.Background(), spec)
	if denied.Status != StatusAuthorizationDenied {
		t.Fatalf("status=%q want %q", denied.Status, StatusAuthorizationDenied)
	}
	if denied.Retryable {
		t.Fatal("an authorization denial was reported retryable")
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, EventRunFailed) {
		t.Fatal("denial did not write a terminal run_failed event")
	}

	// Healing the dependency must not resurrect a denied run.
	authority.healed = true
	replayed := runtime.Execute(context.Background(), spec)
	if replayed.Status != StatusAuthorizationDenied {
		t.Fatalf("terminal denial changed on resume: %+v", replayed)
	}
	if model.calls != 0 || tools.calls != 0 {
		t.Fatalf("resume of a denied run produced side effects: provider=%d tool=%d", model.calls, tools.calls)
	}
}

// StatusAuthorityUnavailable must never be accepted as a terminal event status.
func TestAuthorityUnavailableIsNeverATerminalStatus(t *testing.T) {
	for _, eventType := range []EventType{EventRunCompleted, EventRunFailed, EventRunLimitReached, EventRunCancelled} {
		if terminalStatusMatches(eventType, StatusAuthorityUnavailable) {
			t.Fatalf("%q accepted authority_unavailable as a terminal status", eventType)
		}
	}
}
