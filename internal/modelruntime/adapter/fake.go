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
func (*Fake) Descriptor() modelruntime.AdapterDescriptor {
	return modelruntime.AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "fake", AdapterVersion: 2,
		Transport: modelruntime.TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte("test.fake:endpoint")),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte("test.fake:credential")),
	}
}
func (*Fake) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ProviderID != "test.fake" || request.ProviderModelID == "" || request.Deadline.IsZero() {
		return modelruntime.ErrInvalidRequest
	}
	return nil
}
func (*Fake) Dispatch(ctx context.Context, req modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	visibleInput := req.RenderedContext
	if len(req.ModelInput.CanonicalBytes) > 0 {
		visibleInput = req.ModelInput.CanonicalBytes
	}
	if strings.Contains(string(visibleInput), "[fake-block]") || strings.Contains(req.ProviderModelID, "fake-block") {
		<-ctx.Done()
		return modelruntime.RawResponse{CancellationConfirmed: true}, ctx.Err()
	}
	hash := modelruntime.SHA256Bytes(append(append([]byte{}, visibleInput...), []byte(fmt.Sprintf("|%d|%s", req.InvocationID, req.ProviderModelID))...))
	response := modelruntime.RawResponse{ProviderRequestID: "fake-" + hash[:16], InputTokens: int64(len(visibleInput) / 4), OutputTokens: 16, ProviderReported: false, HiddenReasoning: []byte("hidden fake reasoning must never persist")}
	if req.OutputMode == modelruntime.OutputJSON {
		body, _ := json.Marshal(map[string]any{"context_hash": req.ContextRenderedHash, "invocation_id": req.InvocationID, "provider": "test.fake"})
		response.Content = body
	} else {
		response.Content = []byte("fake:" + hash[:24])
	}
	if strings.Contains(string(visibleInput), "[fake-tool-intent]") {
		response.ToolIntents = []modelruntime.RawToolIntent{{ID: "fake-call-1", Name: "fake.inspect", Arguments: []byte(`{"read_only":true}`)}}
	}
	response.ProviderOutcome = modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     response.ProviderRequestID, HTTPStatus: 200,
		ResponseHash:          modelruntime.SHA256Bytes(response.Content),
		ResponseSchemaVersion: "test.fake.response.v1",
	}
	return response, nil
}
