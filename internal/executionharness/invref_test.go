package executionharness

import (
	"errors"
	"testing"
)

// A model call that fails after its invocation exists must say which one.
//
// The reference died at the only place that held it: the adapter created the
// invocation, dispatched it, and on failure returned a bare error. So the
// Harness recorded a failed run pointing at nothing, and the Executive --
// which decides retryability by asking Model Runtime about a specific
// invocation -- had nothing to ask about. Model Runtime had already recorded
// that the failure was transient. The question could not be formed.
func TestAFailureCarriesTheInvocationItMade(t *testing.T) {
	cause := errors.New("stream_provider_error.resource-exhausted")
	err := WithInvocationRef("154", cause)

	if got := InvocationRefOf(err); got != "154" {
		t.Fatalf("the reference must survive the failure, got %q", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("the cause must survive too: the reference is added, not substituted")
	}
	if err.Error() != cause.Error() {
		t.Fatalf("the message must stay the cause's own: %q", err.Error())
	}
}

// Naming an invocation that does not exist would be worse than naming none:
// the Executive would ask about it and get an answer about nothing.
func TestAFailureBeforeAnyInvocationNamesNothing(t *testing.T) {
	cause := errors.New("context snapshot unavailable")
	for _, ref := range []string{"", "   ", "\t"} {
		err := WithInvocationRef(ref, cause)
		if InvocationRefOf(err) != "" {
			t.Errorf("an empty reference must not be attached")
		}
		if !errors.Is(err, cause) {
			t.Error("the cause must be returned unchanged")
		}
	}
}

func TestNoErrorStaysNoError(t *testing.T) {
	if err := WithInvocationRef("154", nil); err != nil {
		t.Fatalf("success must not become a failure: %v", err)
	}
}

// It must survive wrapping, because the failure travels up through layers
// that add their own context.
func TestTheReferenceSurvivesWrapping(t *testing.T) {
	err := WithInvocationRef("154", errors.New("boom"))
	wrapped := errors.Join(errors.New("dispatching model call"), err)
	if got := InvocationRefOf(wrapped); got != "154" {
		t.Fatalf("the reference must survive a wrap, got %q", got)
	}
}

// An error that never carried a reference reports none rather than guessing.
func TestAnUnrelatedErrorCarriesNoReference(t *testing.T) {
	if got := InvocationRefOf(errors.New("nothing to do with invocations")); got != "" {
		t.Fatalf("got %q", got)
	}
}
