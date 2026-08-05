package modelruntime

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizerStripsHiddenReasoningAndCanonicalizesJSON(t *testing.T) {
	inv := Invocation{ID: 1, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`)}
	raw := RawResponse{Content: []byte(`{"ok":true}`), HiddenReasoning: bytes.Repeat([]byte("secret"), 10), ToolIntents: []RawToolIntent{{Name: "read.only", Arguments: []byte(`{"z":1,"a":2}`)}}}
	got, err := (Normalizer{MaxResponseBytes: 1024, MaxToolIntents: 2}).Normalize(inv, 2, raw)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(got)
	if bytes.Contains(body, []byte("secret")) {
		t.Fatal("hidden reasoning persisted")
	}
	if string(got.Result.ToolIntents[0].Arguments) != `{"a":2,"z":1}` {
		t.Fatalf("arguments not canonical: %s", got.Result.ToolIntents[0].Arguments)
	}
}
func TestNormalizerRejectsSchemaMismatchAndTrailingJSON(t *testing.T) {
	inv := Invocation{ID: 1, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)}
	n := Normalizer{MaxResponseBytes: 1024, MaxToolIntents: 0}
	if _, err := n.Normalize(inv, 1, RawResponse{Content: []byte(`{"ok":"yes"}`)}); err == nil {
		t.Fatal("expected schema mismatch")
	}
	if _, err := n.Normalize(inv, 1, RawResponse{Content: []byte(`{} {}`)}); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
}
func TestPrepareCreateRejectsCrossRoleAndPastDeadline(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	base := CreateInvocationCommand{OrganizationID: "explorarte", TaskID: 1, AttemptID: 1, DispatchActorRoleID: "a/x", SubjectRoleID: "a/y", ContextSnapshotID: 1, Purpose: "test", OutputMode: OutputText, MaxOutputTokens: 10, ThinkingMode: ThinkingDisabled, IdempotencyKey: "x", Deadline: now.Add(1)}
	if _, _, _, err := PrepareCreateCommand(base, now); err == nil {
		t.Fatal("expected cross role rejection")
	}
	base.SubjectRoleID = base.DispatchActorRoleID
	base.Deadline = now.Add(-1)
	if _, _, _, err := PrepareCreateCommand(base, now); err == nil {
		t.Fatal("expected deadline rejection")
	}
}

func TestNormalizerCountsToolIntentBytesAndRejectsTooMany(t *testing.T) {
	inv := Invocation{ID: 1, OutputMode: OutputText}
	n := Normalizer{MaxResponseBytes: 16, MaxToolIntents: 1}
	if _, err := n.Normalize(inv, 1, RawResponse{Content: []byte("ok"), ToolIntents: []RawToolIntent{{Name: "fake.inspect", Arguments: []byte(`{"long":true}`)}}}); err == nil {
		t.Fatal("expected combined response byte limit")
	}
	if _, err := (Normalizer{MaxResponseBytes: 1024, MaxToolIntents: 0}).Normalize(inv, 1, RawResponse{ToolIntents: []RawToolIntent{{Name: "fake.inspect", Arguments: []byte(`{}`)}}}); err == nil {
		t.Fatal("expected tool intent limit")
	}
}
