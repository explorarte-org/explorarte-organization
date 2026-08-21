package runtimeadapter

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

type fixedHistory struct{ events []executionharness.Event }

func (h fixedHistory) Read(context.Context, string) ([]executionharness.Event, error) {
	return h.events, nil
}

func (h fixedHistory) Append(context.Context, string, uint64, executionharness.Event) (executionharness.Event, error) {
	return executionharness.Event{}, nil
}

// The reference was readable exactly when it did not matter and absent
// exactly when it did: only a recorded RESPONSE carried it, and a failed call
// records no response. So every model failure reached the Executive with no
// invocation to ask about, and retryability defaulted to no.
func TestAFailedRunStillNamesItsInvocation(t *testing.T) {
	harness := Harness{History: fixedHistory{events: []executionharness.Event{
		{Type: executionharness.EventRunStarted},
		{Type: executionharness.EventModelRequestPrepared, RequestDigest: "abc"},
		{Type: executionharness.EventRunFailed, TerminalStatus: executionharness.StatusModelError, InvocationRef: "154"},
	}}}
	id, err := harness.lastInvocationID(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if id != 154 {
		t.Fatalf("a failed run must still name the invocation it made, got %d", id)
	}
}

// The recorded-response path must keep working exactly as before.
func TestARecordedResponseStillNamesItsInvocation(t *testing.T) {
	harness := Harness{History: fixedHistory{events: []executionharness.Event{
		{Type: executionharness.EventModelResponseRecorded, ModelResult: &executionharness.ModelResult{InvocationRef: "77"}},
	}}}
	id, err := harness.lastInvocationID(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if id != 77 {
		t.Fatalf("got %d", id)
	}
}

// A run that failed before any invocation existed names none, and that must
// stay distinguishable from naming a wrong one.
func TestARunThatNeverInvokedNamesNothing(t *testing.T) {
	harness := Harness{History: fixedHistory{events: []executionharness.Event{
		{Type: executionharness.EventRunStarted},
		{Type: executionharness.EventRunFailed, TerminalStatus: executionharness.StatusAuthorityUnavailable},
	}}}
	id, err := harness.lastInvocationID(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if id != 0 {
		t.Fatalf("nothing to name means zero, got %d", id)
	}
}
