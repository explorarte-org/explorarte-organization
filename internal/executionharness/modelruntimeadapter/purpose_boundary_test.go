package modelruntimeadapter

import (
	"time"

	"context"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"strings"
	"testing"
)

// The second half of the ambiguity-identity wire: whatever Purpose a caller
// states in Config must reach CreateInvocationCommand.Purpose BYTE-EXACT.
// The ambiguity reconciler classifies against that durable string; any
// transformation here would silently move every new invocation outside the
// authorized set. The legacy fallback for callers that state no purpose is
// pinned too, so it cannot drift into something that accidentally matches.

func TestConfigPurposeReachesTheDurableInvocationByteExact(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}
	adapter, err := New(creator, dispatcher, ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }), Config{
		MaxOutputTokens: 256,
		ThinkingMode:    modelruntime.ThinkingDisabled,
		InvocationTTL:   time.Hour,
		Purpose:         "design_adjudication",
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
	if got := creator.commands[0].Purpose; got != "design_adjudication" {
		t.Fatalf("durable purpose = %q, want byte-exact %q", got, "design_adjudication")
	}
}

func TestEmptyConfigPurposeKeepsTheLegacyProjectionFormat(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "done", nil)}}
	adapter, err := New(creator, dispatcher, ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }), Config{
		MaxOutputTokens: 256,
		ThinkingMode:    modelruntime.ThinkingDisabled,
		InvocationTTL:   time.Hour,
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
	purpose := creator.commands[0].Purpose
	if !strings.HasPrefix(purpose, "execution harness turn ") || len(purpose) != len("execution harness turn ")+64 {
		t.Fatalf("legacy fallback format drifted: %q", purpose)
	}
}
