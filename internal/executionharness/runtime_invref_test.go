package executionharness

import (
	"context"
	"errors"
	"testing"
)

// The durable record of a failed run must name the invocation it made.
//
// It did not, and the consequence was specific: retryability is decided by
// asking Model Runtime about an invocation, and a failed run pointing at
// nothing cannot have that question asked. A campaign died at its adversarial
// review on a provider capacity error that Model Runtime had already recorded
// as transient, with two of three attempts unused.
func TestAFailedRunRecordsTheInvocationItMade(t *testing.T) {
	spec := baseSpec()
	cause := WithInvocationRef("154", errors.New("stream_provider_error.resource-exhausted"))
	model := &fakeModel{results: []ModelResult{{FinishReason: FinishFinal, FinalOutput: "{}"}}, errors: map[int]error{1: cause}}
	store := NewMemoryHistoryStore()
	runtime := newTestRuntime(t, &fakeAuthority{}, model, &fakeToolExecutor{}, fakeCatalog{}, store)

	if got := runtime.Execute(context.Background(), spec); got.Status != StatusModelError {
		t.Fatalf("status=%v", got.Status)
	}
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != EventRunFailed {
			continue
		}
		if event.InvocationRef != "154" {
			t.Fatalf("the failed run must name its invocation, got %q", event.InvocationRef)
		}
		return
	}
	t.Fatal("no run_failed event was recorded")
}

// A failure that never reached an invocation names none. Recording a
// reference that does not exist would send the Executive to ask about
// nothing, which is worse than it asking about nothing at all.
func TestAFailureBeforeAnyInvocationRecordsNoReference(t *testing.T) {
	spec := baseSpec()
	model := &fakeModel{results: []ModelResult{{FinishReason: FinishFinal, FinalOutput: "{}"}}, errors: map[int]error{1: errors.New("context snapshot unavailable")}}
	store := NewMemoryHistoryStore()
	runtime := newTestRuntime(t, &fakeAuthority{}, model, &fakeToolExecutor{}, fakeCatalog{}, store)

	runtime.Execute(context.Background(), spec)
	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == EventRunFailed && event.InvocationRef != "" {
			t.Fatalf("nothing was invoked, so nothing may be named: %q", event.InvocationRef)
		}
	}
}
