package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type Fake struct{}

func NewFake() *Fake             { return &Fake{} }
func (*Fake) ProviderID() string { return "test.fake" }
func (*Fake) Dispatch(ctx context.Context, req modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	if strings.Contains(string(req.RenderedContext), "[fake-block]") || strings.Contains(req.ProviderModelID, "fake-block") {
		<-ctx.Done()
		return modelruntime.RawResponse{CancellationConfirmed: true}, ctx.Err()
	}
	hash := modelruntime.SHA256Bytes(append(append([]byte{}, req.RenderedContext...), []byte(fmt.Sprintf("|%d|%s", req.InvocationID, req.ProviderModelID))...))
	response := modelruntime.RawResponse{ProviderRequestID: "fake-" + hash[:16], InputTokens: int64(len(req.RenderedContext) / 4), OutputTokens: 16, ProviderReported: false, HiddenReasoning: []byte("hidden fake reasoning must never persist")}
	if req.OutputMode == modelruntime.OutputJSON {
		body, _ := json.Marshal(map[string]any{"context_hash": req.ContextRenderedHash, "invocation_id": req.InvocationID, "provider": "test.fake"})
		response.Content = body
	} else {
		response.Content = []byte("fake:" + hash[:24])
	}
	if strings.Contains(string(req.RenderedContext), "[fake-tool-intent]") {
		response.ToolIntents = []modelruntime.RawToolIntent{{Name: "fake.inspect", Arguments: []byte(`{"read_only":true}`)}}
	}
	return response, nil
}
