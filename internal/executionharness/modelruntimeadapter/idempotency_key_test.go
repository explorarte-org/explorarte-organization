package modelruntimeadapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// legacyKey is the shape every runtime before execution-contract instructions
// wrote into model_invocations. It is spelled out here rather than derived, so
// a change to the production function fails this test instead of silently
// agreeing with itself.
func legacyKey(canonicalDigest, outputContract string) string {
	return "execution-harness:" + canonicalDigest + ":" + outputContract
}

func keyAdapter(t *testing.T, contract string) *Adapter {
	t.Helper()
	value, err := New(&fakeCreator{}, &fakeDispatcher{},
		ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }),
		Config{MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled,
			InvocationTTL: time.Hour, OutputMode: modelruntime.OutputText,
			ExecutionContractInstructions: contract})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// A + B: a run without an execution contract keeps the historical key
// byte-for-byte. This is the property that keeps invocations created by
// earlier runtimes adoptable.
func TestNoContractKeyKeepsTheHistoricalShape(t *testing.T) {
	adapter := keyAdapter(t, "")
	projection := digest([]byte("projection"))

	got := adapter.idempotencyKey(projection)
	want := legacyKey(projection, adapter.outputContract)
	if got != want {
		t.Fatalf("no-contract key changed:\n got: %q\nwant: %q", got, want)
	}
	if strings.Count(got, ":") != 2 {
		t.Fatalf("historical shape has exactly two separators: %q", got)
	}
	if len(got) != len("execution-harness:")+64+1+64 {
		t.Fatalf("historical key length changed: %d", len(got))
	}
}

// C: the contract case is the only one that was oversized, and it is now
// inside the bound.
func TestContractKeyStaysWithinModelRuntimeBound(t *testing.T) {
	const bound = 200
	adapter := keyAdapter(t, strings.Repeat("task_class guidance ", 400))
	got := adapter.idempotencyKey(digest([]byte("projection")))
	if len(got) > bound {
		t.Fatalf("key is %d bytes, past the %d-byte bound: %q", len(got), bound, got)
	}
	if len(got) != len("execution-harness:")+64 {
		t.Fatalf("folded key is not a fixed-length digest: %q", got)
	}
	// The legacy representation of the SAME run is what broke: assert the
	// arithmetic directly so the reason survives.
	legacy := legacyKey(digest([]byte("projection")), adapter.outputContract) + ":" + adapter.executionContractKey
	if len(legacy) <= bound {
		t.Fatalf("the legacy three-component form is %d bytes; this test no longer covers the defect", len(legacy))
	}
}

// D, E, F, G: every component still decides the key.
func TestEveryComponentStillDecidesTheKey(t *testing.T) {
	projection := digest([]byte("projection"))
	none := keyAdapter(t, "")
	contractA := keyAdapter(t, "contract A")
	contractB := keyAdapter(t, "contract B")

	keyNone := none.idempotencyKey(projection)
	keyA := contractA.idempotencyKey(projection)
	keyB := contractB.idempotencyKey(projection)

	if keyA == keyB {
		t.Fatal("different execution contracts produced the same key")
	}
	if keyA == keyNone || keyB == keyNone {
		t.Fatal("a contract run shares the no-contract key")
	}
	if other := none.idempotencyKey(digest([]byte("other projection"))); other == keyNone {
		t.Fatal("a different projection produced the same key")
	}
	if other := contractA.idempotencyKey(digest([]byte("other projection"))); other == keyA {
		t.Fatal("a different projection produced the same key under a contract")
	}
	// A different output contract must change both shapes.
	otherOutput := *none
	otherOutput.outputContract = digest([]byte("a different output contract"))
	if otherOutput.idempotencyKey(projection) == keyNone {
		t.Fatal("a different output contract produced the same no-contract key")
	}
	otherOutputContract := *contractA
	otherOutputContract.outputContract = digest([]byte("a different output contract"))
	if otherOutputContract.idempotencyKey(projection) == keyA {
		t.Fatal("a different output contract produced the same contract key")
	}
}

// H: the one that matters operationally. An invocation durably created by an
// earlier runtime under the historical key is still found and adopted by this
// adapter -- no second invocation, no second dispatch. This is the property
// that folding the compatible case would have broken.
func TestHistoricalInvocationIsAdoptedNotRecreated(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	durable := successfulDispatch(7, "recovered answer", nil)
	invocation := durable.Invocation
	creator := &fakeCreator{found: &invocation, input: storedInput(request, 41), outcome: durable}
	dispatcher := &fakeDispatcher{}
	adapter := newAdapter(t, creator, dispatcher)

	// The adapter under test has no execution contract, so the key it derives
	// must be the historical one an older runtime would have written.
	historical := legacyKey(request.CanonicalDigest, adapter.outputContract)
	if got := adapter.idempotencyKey(request.CanonicalDigest); got != historical {
		t.Fatalf("adapter derives %q, want the historical key %q", got, historical)
	}

	result, err := adapter.Invoke(context.Background(), spec.Identity, request)
	if err != nil {
		t.Fatalf("the historical invocation was not adoptable: %v", err)
	}
	if result.FinalOutput != "recovered answer" {
		t.Fatalf("final output=%q", result.FinalOutput)
	}
	if len(creator.commands) != 0 {
		t.Fatalf("a second invocation was created: %+v", creator.commands)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("a second dispatch was issued: %v", dispatcher.calls)
	}
	if len(creator.findKeys) == 0 || creator.findKeys[0] != historical {
		t.Fatalf("the adapter looked up %v, want %q", creator.findKeys, historical)
	}
}
