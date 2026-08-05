package adapter

import (
	"context"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"strings"
	"testing"
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
