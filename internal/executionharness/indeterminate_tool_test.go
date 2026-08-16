package executionharness

import (
	"context"
	"errors"
	"testing"
)

// crashAfterTool reproduces the one window that reaches outside the system:
// the tool has already run and produced its external effect, and the process
// or the database dies before that fact becomes durable.
type crashAfterTool struct {
	inner *MemoryHistoryStore
	armed bool
}

func (s *crashAfterTool) Append(ctx context.Context, runID string, expected uint64, event Event) (Event, error) {
	if s.armed && event.Type == EventToolResultRecorded {
		return Event{}, errors.New("simulated loss of durability after the tool ran")
	}
	return s.inner.Append(ctx, runID, expected, event)
}

func (s *crashAfterTool) Read(ctx context.Context, runID string) ([]Event, error) {
	return s.inner.Read(ctx, runID)
}

func TestToolExecutedButUnrecordedIsNeverRunAgain(t *testing.T) {
	spec := baseSpec()
	store := &crashAfterTool{inner: NewMemoryHistoryStore(), armed: true}

	// Attempt 1: the tool runs, then durability is lost.
	modelA := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, InvocationRef: "inv-1", ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}},
	}}
	toolsA := &fakeToolExecutor{}
	runtimeA, err := New(&flakyAuthority{}, modelA, catalogWithFixture(), toolsA, store)
	if err != nil {
		t.Fatal(err)
	}
	crashed := runtimeA.Execute(context.Background(), spec)
	if toolsA.calls != 1 {
		t.Fatalf("setup: the tool ran %d times, want 1", toolsA.calls)
	}
	if crashed.Status != StatusHistoryError {
		t.Fatalf("setup: crashed=%+v want history_error", crashed)
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if call, found := unresolvedToolCall(events); !found || call != "call-1" {
		t.Fatalf("setup: history does not hold an unresolved tool call: %v %v", call, found)
	}

	// Attempt 2: a fresh process with a healthy store. The external side effect
	// may already exist and nothing in the record can say whether it does, so
	// the run must refuse rather than run the tool a second time.
	store.armed = false
	modelB := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, InvocationRef: "inv-2", ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}},
		{FinishReason: FinishFinal, FinalOutput: "must never be reached", InvocationRef: "inv-3"},
	}}
	toolsB := &fakeToolExecutor{}
	runtimeB, err := New(&flakyAuthority{}, modelB, catalogWithFixture(), toolsB, store)
	if err != nil {
		t.Fatal(err)
	}
	resumed := runtimeB.Execute(context.Background(), spec)

	if toolsB.calls != 0 {
		t.Fatalf("the tool ran %d more times after the crash: an external side effect was duplicated", toolsB.calls)
	}
	if modelB.calls != 0 {
		t.Fatalf("the resumed run called the provider %d times before resolving the unknown tool outcome", modelB.calls)
	}
	if resumed.Status != StatusIndeterminateToolExecution {
		t.Fatalf("resumed=%+v want indeterminate_tool_execution", resumed)
	}
	if resumed.Retryable {
		t.Fatal("an indeterminate tool outcome was reported retryable")
	}

	// The verdict is durable: a later attempt reproduces it with no side effects.
	toolsC := &fakeToolExecutor{}
	modelC := &fakeModel{}
	runtimeC, err := New(&flakyAuthority{}, modelC, catalogWithFixture(), toolsC, store)
	if err != nil {
		t.Fatal(err)
	}
	again := runtimeC.Execute(context.Background(), spec)
	if again.Status != StatusIndeterminateToolExecution || toolsC.calls != 0 || modelC.calls != 0 {
		t.Fatalf("terminal replay=%+v tool=%d model=%d", again, toolsC.calls, modelC.calls)
	}
}

// The complement: a tool call the executor never reached leaves no durable
// request at all, so the run stays cleanly resumable. Without this the fix
// above would be indistinguishable from "any interrupted run is dead".
func TestToolNeverReachedLeavesNoDurableRequest(t *testing.T) {
	spec := baseSpec()
	// Authority call 1 admits the turn; call 2 is the tool boundary and fails.
	authority := &flakyAuthority{failAt: 2, failErr: unavailable()}
	model := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, InvocationRef: "inv-1", ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}},
		{FinishReason: FinishTools, InvocationRef: "inv-2", ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}},
		{FinishReason: FinishFinal, FinalOutput: "resumed final", InvocationRef: "inv-3"},
	}}
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, authority, model, tools, catalogWithFixture(), store)

	interrupted := runtime.Execute(context.Background(), spec)
	if interrupted.Status != StatusAuthorityUnavailable || tools.calls != 0 {
		t.Fatalf("interrupted=%+v tool=%d", interrupted, tools.calls)
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if call, found := unresolvedToolCall(events); found {
		t.Fatalf("a tool that never ran left a durable request %q, which would strand the run", call)
	}

	authority.healed = true
	resumed := runtime.Execute(context.Background(), spec)
	if resumed.Status != StatusCompleted || tools.calls != 1 {
		t.Fatalf("resumed=%+v tool=%d: the run should have continued normally", resumed, tools.calls)
	}
}
