package modelruntime

import (
	"errors"
	"testing"
)

func validAdapterDescriptor() AdapterDescriptor {
	return AdapterDescriptor{
		ProviderID: "openai_compatible", AdapterID: "openai_chat_completions", AdapterVersion: 1,
		Transport: TransportHTTP, RequestSchemaVersion: "openai.chat.completions.request.v1",
		ResponseSchemaVersion: "openai.chat.completions.response.v1",
		EndpointFingerprint:   SHA256Bytes([]byte("https://example.test/v1/chat/completions")),
		CredentialRefHash:     SHA256Bytes([]byte("/run/secrets/provider-token")),
	}
}

func TestAdapterDescriptorValidation(t *testing.T) {
	value := validAdapterDescriptor()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	value.EndpointFingerprint = "raw-endpoint"
	if err := value.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v", err)
	}
}

func TestProviderOutcomeValidation(t *testing.T) {
	valid := []ProviderOutcome{
		{OutcomeClassification: ProviderOutcomeResponseReceived, HTTPStatus: 200, ResponseHash: SHA256Bytes([]byte("response")), ResponseSchemaVersion: "v1"},
		{OutcomeClassification: ProviderOutcomeRejected, HTTPStatus: 429, ErrorClass: "rate_limit", ErrorCode: "rate_limited", Retryable: true, ResponseHash: SHA256Bytes([]byte("rejection")), ResponseSchemaVersion: "v1"},
		{OutcomeClassification: ProviderOutcomeNotSent, ErrorClass: "credential", ErrorCode: "credential_unavailable", ResponseSchemaVersion: "v1"},
		{OutcomeClassification: ProviderOutcomeAmbiguous, ErrorClass: "transport", ErrorCode: "transport_timeout", Retryable: true, ResponseSchemaVersion: "v1"},
		{OutcomeClassification: ProviderOutcomeCancelled, ErrorCode: "cancelled", ResponseSchemaVersion: "v1", CancellationConfirmed: true},
	}
	for index, outcome := range valid {
		if err := outcome.Validate(); err != nil {
			t.Fatalf("case %d: %v", index, err)
		}
	}
	invalid := valid[0]
	invalid.ResponseHash = ""
	if err := invalid.Validate(); err == nil {
		t.Fatal("successful outcome without response hash was accepted")
	}
	unsafe := valid[1]
	unsafe.ErrorCode = "provider message leaked"
	if err := unsafe.Validate(); err == nil {
		t.Fatal("unsafe provider error metadata was accepted")
	}
	unsafe = valid[0]
	unsafe.ProviderRequestID = "request\nheader"
	if err := unsafe.Validate(); err == nil {
		t.Fatal("unsafe provider request ID was accepted")
	}
}

func TestAdapterErrorDoesNotExposeCauseText(t *testing.T) {
	err := &AdapterError{Phase: AdapterFailureAmbiguous, Outcome: ProviderOutcome{ErrorClass: "transport", ErrorCode: "transport_error"}, Cause: errors.New("secret provider details")}
	if got := err.Error(); got != "model provider adapter error: ambiguous_after_request: transport: transport_error" {
		t.Fatalf("error=%q", got)
	}
	classified, ok := AsAdapterError(err)
	if !ok || classified != err {
		t.Fatal("adapter error was not classifiable")
	}
}
