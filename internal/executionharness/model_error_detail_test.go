package executionharness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A run that cannot say why it failed cannot be diagnosed from its own durable
// record, which is the record's whole purpose. The invocation error used to be
// discarded outright, so every model failure -- an expired credential, a
// denied egress scope, a rejected schema, a provider timeout -- produced the
// same opaque sentence.
func TestModelFailureCausePersistsIntoHistoryAndResult(t *testing.T) {
	spec := baseSpec()
	cause := "resolve price tier: no pricing row for provider xai model grok-4.6"
	model := &fakeModel{results: []ModelResult{{FinishReason: FinishFinal, FinalOutput: "{}"}}, errors: map[int]error{1: errors.New(cause)}}
	store := NewMemoryHistoryStore()
	runtime := newTestRuntime(t, &fakeAuthority{}, model, &fakeToolExecutor{}, fakeCatalog{}, store)

	got := runtime.Execute(context.Background(), spec)
	if got.Status != StatusModelError {
		t.Fatalf("status=%v", got.Status)
	}
	if !strings.Contains(got.TerminationReason, cause) {
		t.Fatalf("the result lost the cause: %q", got.TerminationReason)
	}
	// The historical prefix is kept so existing consumers still match on it.
	if !strings.HasPrefix(got.TerminationReason, "model execution failed") {
		t.Fatalf("the reason lost its stable prefix: %q", got.TerminationReason)
	}

	events, err := store.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == EventRunFailed && strings.Contains(event.Reason, cause) {
			found = true
		}
	}
	if !found {
		t.Fatalf("durable history did not record the cause: %+v", events)
	}
}

// The reason is bounded, so no provider response body can be smuggled into
// durable history through it.
func TestModelFailureReasonIsBounded(t *testing.T) {
	long := strings.Repeat("x", maxModelFailureReasonBytes*3)
	reason := modelFailureReason(errors.New(long))
	if len(reason) > maxModelFailureReasonBytes+len("model execution failed: ") {
		t.Fatalf("reason is unbounded: %d bytes", len(reason))
	}
	// A nil or empty cause degrades to the original sentence rather than to a
	// dangling colon.
	if got := modelFailureReason(nil); got != "model execution failed" {
		t.Fatalf("nil cause=%q", got)
	}
	if got := modelFailureReason(errors.New("   ")); got != "model execution failed" {
		t.Fatalf("blank cause=%q", got)
	}
}
