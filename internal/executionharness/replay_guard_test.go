package executionharness

import (
	"context"
	"encoding/json"
	"testing"
)

// The replay guard was narrowed to seed only from RESOLVED tool calls, so an
// execution interrupted between proposal and resolution can resume. The
// dangerous reading of that change is that a tool which already ran could be
// proposed again after a restart and execute twice. These two tests pin both
// halves so neither can be satisfied by a loose rule.

func TestResolvedToolCallCannotBeReplayedAfterResume(t *testing.T) {
	spec := baseSpec()
	// Authority call 1 admits turn 1, call 2 admits the tool, call 3 is the
	// outage that ends the attempt with the tool already resolved.
	authority := &flakyAuthority{failAt: 3, failErr: unavailable()}
	model := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, InvocationRef: "inv-1", ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}},
		// After the restart the model proposes the same id whose result is
		// already durable and already visible to it.
		{FinishReason: FinishTools, InvocationRef: "inv-2", ToolRequests: []ToolRequest{toolRequest("call-1", "lookup_fixture")}},
		{FinishReason: FinishFinal, FinalOutput: "must never be reached", InvocationRef: "inv-3"},
	}}
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, authority, model, tools, catalogWithFixture(), store)

	interrupted := runtime.Execute(context.Background(), spec)
	if interrupted.Status != StatusAuthorityUnavailable {
		t.Fatalf("interrupted=%+v", interrupted)
	}
	if tools.calls != 1 {
		t.Fatalf("setup: tool ran %d times, want 1", tools.calls)
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEvent(events, EventToolResultRecorded) {
		t.Fatal("setup: the tool call was not resolved before the outage")
	}

	authority.healed = true
	resumed := runtime.Execute(context.Background(), spec)

	if tools.calls != 1 {
		t.Fatalf("a resolved tool executed %d times after resume: the replay guard was weakened", tools.calls)
	}
	if resumed.Status != StatusToolError {
		t.Fatalf("resumed=%+v want tool_error from the replay guard", resumed)
	}
	after, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !eventHasCode(after, EventToolCallDenied, "tool_call_replay") {
		t.Fatal("the repeated tool call was not denied as a replay")
	}
}

// A denied tool call is also observable by the model, so it must keep seeding
// the guard even though it never executed.
func TestDeniedToolCallStillSeedsTheReplayGuard(t *testing.T) {
	spec := baseSpec()
	model := &fakeModel{results: []ModelResult{
		{FinishReason: FinishTools, InvocationRef: "inv-1", ToolRequests: []ToolRequest{
			{ToolCallID: "call-9", ToolName: "not_in_catalog", Arguments: json.RawMessage(`{"key":"alpha"}`)},
		}},
	}}
	tools := &fakeToolExecutor{}
	store := NewMemoryHistoryStore()
	runtime := newRuntimeWith(t, &flakyAuthority{}, model, tools, catalogWithFixture(), store)

	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusToolError || tools.calls != 0 {
		t.Fatalf("denied tool call=%+v tool_calls=%d", got, tools.calls)
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !requestedToolCallIDs(events)["call-9"] {
		t.Fatal("a denied tool call was dropped from the replay guard seed")
	}
}

// The permitted half, stated on its own: an unresolved proposal is not a replay.
func TestUnresolvedToolProposalIsNotSeededAsAReplay(t *testing.T) {
	events := []Event{
		{RunID: "run-1", Sequence: 1, Type: EventRunStarted},
		{RunID: "run-1", Sequence: 2, Type: EventToolCallRequested, ToolRequest: &ToolRequest{ToolCallID: "call-unresolved"}},
	}
	if requestedToolCallIDs(events)["call-unresolved"] {
		t.Fatal("a tool call the model never observed was seeded as already used")
	}
}
