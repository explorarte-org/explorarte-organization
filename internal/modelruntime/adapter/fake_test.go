package adapter

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

func TestFakeDeterministicAndOpaque(t *testing.T) {
	a := NewFake()
	req := modelruntime.CanonicalRequest{InvocationID: 1, ProviderModelID: "v1", RenderedContext: []byte("context"), ContextRenderedHash: modelruntime.SHA256Bytes([]byte("context")), OutputMode: modelruntime.OutputText}
	x, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	y, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(x.Content) != string(y.Content) {
		t.Fatal("fake is not deterministic")
	}
	if !strings.Contains(string(x.HiddenReasoning), "never persist") {
		t.Fatal("test requires hidden reasoning fixture")
	}
}

// TestFakeJSONResponseMarker pins the FINANCE_HARNESS_RUNTIME_HOTFIX_V1
// addition: a caller-embedded [fake-json-b64:...] marker anywhere in the
// rendered prompt content makes Dispatch echo that decoded payload back
// verbatim as Content, regardless of OutputMode -- letting a real Model
// Runtime dispatch through this fake adapter return a domain-shaped
// response a consuming service can actually parse.
func TestFakeJSONResponseMarker(t *testing.T) {
	a := NewFake()
	payload := `{"verdict":"recommended","summary":"ok"}`
	marker := "[fake-json-b64:" + base64.StdEncoding.EncodeToString([]byte(payload)) + "]"
	req := modelruntime.CanonicalRequest{
		InvocationID: 1, ProviderModelID: "v1",
		RenderedContext:     []byte("Proposal Data: some prose ... " + marker + " ... more prose"),
		ContextRenderedHash: modelruntime.SHA256Bytes([]byte("x")),
		OutputMode:          modelruntime.OutputText,
	}
	resp, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if string(resp.Content) != payload {
		t.Fatalf("Content = %q, want the decoded marker payload %q", resp.Content, payload)
	}
}

// TestFakeJSONResponseMarker_AbsentFallsBackToDeterministicHash proves the
// new marker check is purely additive: content with no marker still
// produces the original hash-derived placeholder, unchanged.
func TestFakeJSONResponseMarker_AbsentFallsBackToDeterministicHash(t *testing.T) {
	a := NewFake()
	req := modelruntime.CanonicalRequest{InvocationID: 1, ProviderModelID: "v1", RenderedContext: []byte("no marker here"), ContextRenderedHash: modelruntime.SHA256Bytes([]byte("x")), OutputMode: modelruntime.OutputText}
	resp, err := a.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.HasPrefix(string(resp.Content), "fake:") {
		t.Fatalf("Content = %q, want the original fake: hash placeholder", resp.Content)
	}
}
