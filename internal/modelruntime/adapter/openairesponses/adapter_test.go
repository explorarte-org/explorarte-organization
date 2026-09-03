package openairesponses

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

func writeCredential(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-token")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func adapterConfig(endpoint, credential string) Config {
	return Config{
		Enabled: true, EndpointURL: endpoint, CredentialFile: credential,
		RequestTimeout: time.Minute, FailureThreshold: 2, OpenDuration: time.Minute,
		MaxResponseBytes: 1 << 20,
	}
}

func validRequest(deadline time.Time) modelruntime.CanonicalRequest {
	return modelruntime.CanonicalRequest{
		InvocationID: 1, DispatchAttemptID: 2, OrganizationID: "explorarte",
		OrganizationRevisionID: 3, TaskID: 4, AttemptID: 5,
		DispatchActorRoleID: "ingenieria_ia/code-runner", SubjectRoleID: "empresa/ceo",
		ModelProfileID: "ceo-primary", ModelProfileVersionID: 6,
		ProviderID: ProviderID, ProviderModelID: "gpt-5.6-luna",
		ProviderIdempotencyKey: modelruntime.SHA256Bytes([]byte("provider-request")),
		ContextSnapshotID:      7, ContextRenderedHash: modelruntime.SHA256Bytes([]byte("hello")),
		RenderedContext: []byte("hello"), OutputMode: modelruntime.OutputText,
		MaxOutputTokens: 64, ThinkingMode: modelruntime.ThinkingOpaque,
		ReasoningEffort: "xhigh", Deadline: deadline,
	}
}

func TestEncodeRequestCarriesStructuredHistoryAndTools(t *testing.T) {
	request := validRequest(time.Now().Add(time.Minute))
	request.ModelInput = modelruntime.PreparedModelInput{Envelope: modelruntime.ModelInputEnvelope{
		SchemaVersion: modelruntime.ModelInputEnvelopeSchemaV1,
		StablePrefix:  []modelruntime.ModelInputMessage{{Role: modelruntime.ModelInputRoleUser, Content: "hello"}},
		VisibleHistory: []modelruntime.ModelInputMessage{
			{Role: modelruntime.ModelInputRoleAssistant, ToolCalls: []modelruntime.ModelInputToolCall{{ID: "call-1", Name: "lookup_fixture", Arguments: json.RawMessage(`{"id":"x"}`)}}},
			{Role: modelruntime.ModelInputRoleTool, ToolCallID: "call-1", ToolName: "lookup_fixture", Content: `{"value":"ok"}`},
		},
		ToolDefinitions: []modelruntime.ModelInputToolDefinition{{Name: "lookup_fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}}
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload responsesRequest
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) != 3 || payload.Input[1].Type != "function_call" || payload.Input[1].CallID != "call-1" || payload.Input[2].Type != "function_call_output" || payload.Input[2].CallID != "call-1" {
		t.Fatalf("structured Responses input was not preserved: %+v", payload.Input)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "lookup_fixture" {
		t.Fatalf("Responses tool definitions were not preserved: %+v", payload.Tools)
	}
}

func TestConfigRequiresSecureExplicitEndpointAndCredentialReference(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	for name, endpoint := range map[string]string{
		"plain HTTP": "http://example.test/v1/responses",
		"userinfo":   "https://user@example.test/v1/responses",
		"query":      "https://example.test/v1/responses?x=1",
		"fragment":   "https://example.test/v1/responses#x",
		"wrong path": "https://example.test/v1/chat/completions",
	} {
		t.Run(name, func(t *testing.T) {
			if err := adapterConfig(endpoint, credential).Validate(); err == nil {
				t.Fatal("unsafe endpoint unexpectedly accepted")
			}
		})
	}
	cfg := adapterConfig("https://example.test/v1/responses", "relative-token")
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative credential path unexpectedly accepted")
	}
	if err := adapterConfig("https://example.test/v1/responses", credential).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeRequestUsesInputArrayAndNestedReasoningEffort(t *testing.T) {
	request := validRequest(time.Now().Add(time.Minute))
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "gpt-5.6-luna" || payload["stream"] != false || payload["store"] != false {
		t.Errorf("payload=%s", body)
	}
	if _, present := payload["messages"]; present {
		t.Errorf("Responses API request must not carry a Chat Completions messages field: payload=%s", body)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input=%v", payload["input"])
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "xhigh" {
		t.Errorf("reasoning=%v", payload["reasoning"])
	}
	if payload["max_output_tokens"] != float64(64) {
		t.Errorf("max_output_tokens=%v want 64: payload=%s", payload["max_output_tokens"], body)
	}

	noReasoning := validRequest(time.Now().Add(time.Minute))
	noReasoning.ReasoningEffort = ""
	body, err = encodeRequest(noReasoning)
	if err != nil {
		t.Fatal(err)
	}
	var noReasoningPayload map[string]any
	if err = json.Unmarshal(body, &noReasoningPayload); err != nil {
		t.Fatal(err)
	}
	if _, present := noReasoningPayload["reasoning"]; present {
		t.Errorf("non-reasoning request must omit reasoning object: payload=%s", body)
	}
}

func TestDispatchSendsBoundedCanonicalRequestAndNormalizesResponse(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	var called atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-provider-token" {
			t.Errorf("authorization header=%q", got)
		}
		if got := r.Header.Get("X-Client-Request-Id"); len(got) != 64 {
			t.Errorf("client request ID=%q", got)
		}
		w.Header().Set("x-request-id", "provider-request-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fallback-id","object":"response","status":"completed","output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","name":"inspect","call_id":"c1","arguments":{"read_only":true}}],"usage":{"input_tokens":12,"output_tokens":3}}`)
	}))
	defer server.Close()

	cfg := adapterConfig(server.URL+"/v1/responses", credential)
	adapter, err := newAdapter(cfg, server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest(time.Now().Add(time.Minute))
	if err = adapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{ProviderID: request.ProviderID, ProviderModelID: request.ProviderModelID, Deadline: request.Deadline}); err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if called.Load() != 1 || string(response.Content) != "ok" || response.ProviderRequestID != "provider-request-1" || response.InputTokens != 12 || response.OutputTokens != 3 || len(response.ToolIntents) != 1 || response.ToolIntents[0].Name != "inspect" {
		t.Fatalf("response=%+v calls=%d", response, called.Load())
	}
	if response.ProviderOutcome.OutcomeClassification != modelruntime.ProviderOutcomeResponseReceived || response.ProviderOutcome.HTTPStatus != http.StatusOK || len(response.ProviderOutcome.ResponseHash) != 64 {
		t.Fatalf("outcome=%+v", response.ProviderOutcome)
	}
}

func TestDispatchRejectsIncompleteEmptyResponseAsFailure(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"incomplete","incomplete_details":{"reason":"max_tokens"},"output":[{"type":"reasoning"}],"usage":{"input_tokens":76000,"output_tokens":8000}}`)
	}))
	defer server.Close()

	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}

	response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)

	if !ok ||
		classified.Phase != modelruntime.AdapterFailureResponseReceived ||
		!strings.Contains(classified.Outcome.ErrorCode, "max_tokens") ||
		classified.Outcome.Retryable {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}

	if !response.ProviderReported ||
		response.ProviderRequestID != "r1" ||
		response.InputTokens != 76000 ||
		response.OutputTokens != 8000 {
		t.Fatalf("failed response lost provider usage: %+v", response)
	}
}
func TestDispatchAcceptsIncompleteResponseWithPartialContent(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"incomplete","incomplete_details":{"reason":"max_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial but usable"}]}],"usage":{"input_tokens":76000,"output_tokens":8000}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	if err != nil || string(response.Content) != "partial but usable" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestDispatchRejectsIncompleteJSONWithPartialContentAndPreservesUsage(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]}],"usage":{"input_tokens":76000,"output_tokens":8000}}`)
	}))
	defer server.Close()

	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest(time.Now().Add(time.Minute))
	request.OutputMode = modelruntime.OutputJSON
	request.OutputSchema = []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)

	response, err := adapter.Dispatch(context.Background(), request)
	classified, ok := modelruntime.AsAdapterError(err)

	if !ok ||
		classified.Phase != modelruntime.AdapterFailureResponseReceived ||
		classified.Outcome.ErrorCode != "response_incomplete_max_output_tokens" ||
		classified.Outcome.Retryable {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}

	if !response.ProviderReported ||
		response.ProviderRequestID != "r1" ||
		response.InputTokens != 76000 ||
		response.OutputTokens != 8000 {
		t.Fatalf("incomplete JSON failure lost provider usage: %+v", response)
	}
}
func TestDispatchClassifiesProviderRejectionWithoutLeakingMessage(t *testing.T) {
	credential := writeCredential(t, "super-secret-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "rejected-1")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_value","message":"sensitive provider message"}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Phase != modelruntime.AdapterFailureResponseReceived || classified.Outcome.HTTPStatus != http.StatusBadRequest || classified.Outcome.ErrorClass != "invalid_request_error" || classified.Outcome.ErrorCode != "invalid_value" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	text := err.Error()
	if strings.Contains(text, "sensitive provider message") || strings.Contains(text, "super-secret-provider-token") {
		t.Fatalf("secret or provider message leaked: %s", text)
	}
}

func TestDispatchRejectsTopLevelErrorObjectAsFailure(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"failed","error":{"type":"server_error","message":"sensitive"},"output":[],"usage":{}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "server_error" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
}

func TestTransportFailureIsAmbiguousAndCircuitOpens(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with provider details")
	})}
	adapter, err := newAdapter(adapterConfig("https://example.test/v1/responses", credential), client, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest(now.Add(time.Minute))
	for i := 0; i < 2; i++ {
		_, dispatchErr := adapter.Dispatch(context.Background(), request)
		classified, ok := modelruntime.AsAdapterError(dispatchErr)
		if !ok || classified.Phase != modelruntime.AdapterFailureAmbiguous || classified.Outcome.OutcomeClassification != modelruntime.ProviderOutcomeAmbiguous {
			t.Fatalf("dispatch error=%v classified=%+v", dispatchErr, classified)
		}
	}
	preflightErr := adapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{ProviderID: ProviderID, ProviderModelID: request.ProviderModelID, Deadline: request.Deadline})
	classified, ok := modelruntime.AsAdapterError(preflightErr)
	if !ok || classified.Outcome.ErrorCode != "circuit_open" || classified.Outcome.OutcomeClassification != modelruntime.ProviderOutcomeNotSent {
		t.Fatalf("preflight error=%v classified=%+v", preflightErr, classified)
	}
}

func TestRedirectIsRejectedAsAmbiguousAfterRequest(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Phase != modelruntime.AdapterFailureAmbiguous {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// --- Gate F: Provider Failure Telemetry ------------------------------------
//
// Mirrors internal/modelruntime/adapter/deepseek's Gate F test suite,
// adapted to the Responses API's status/incomplete_details shape instead of
// a chat-completions finish_reason.

func TestGateFTelemetryOnIncompleteMaxOutputTokens(t *testing.T) {
	// This is exactly the shape of this organization's own live incident:
	// gpt-5.6-luna (validRequest's own ProviderModelID) consumed its entire
	// output budget on invisible reasoning, status=="incomplete",
	// incomplete_details.reason=="max_output_tokens", zero visible text --
	// billed anyway. Before Gate F parity here, none of this was visible on
	// the outcome row without a raw response dump.
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"reasoning"}],"usage":{"input_tokens":14337,"output_tokens":2800}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok {
		t.Fatalf("err=%v", err)
	}
	outcome := classified.Outcome
	if outcome.FinishReason != "incomplete:max_output_tokens" {
		t.Fatalf("expected finish_reason=incomplete:max_output_tokens, got %q", outcome.FinishReason)
	}
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 14337 || outcome.OutputTokens == nil || *outcome.OutputTokens != 2800 {
		t.Fatalf("expected usage recovered from the decoded envelope: outcome=%+v", outcome)
	}
	if outcome.ResponseFormat != "text" || outcome.MaxOutputTokens == nil || *outcome.MaxOutputTokens != 64 {
		t.Fatalf("expected request-shaping telemetry from the CanonicalRequest: outcome=%+v", outcome)
	}
	if outcome.RequestDuration == nil || *outcome.RequestDuration < 0 {
		t.Fatalf("expected a non-negative request duration, got %+v", outcome.RequestDuration)
	}
}

func TestGateFTelemetryOnResponseReadFailed(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
	}))
	defer server.Close()
	cfg := adapterConfig(server.URL+"/v1/responses", credential)
	cfg.MaxResponseBytes = 1024
	adapter, err := newAdapter(cfg, server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_read_failed" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	if outcome.ResponseContentBytes == nil || *outcome.ResponseContentBytes <= 0 {
		t.Fatalf("expected a positive response content byte length, got %+v", outcome.ResponseContentBytes)
	}
	if outcome.UsageAvailable || outcome.InputTokens != nil || outcome.OutputTokens != nil {
		t.Fatalf("usage must be unavailable before any decode was attempted: outcome=%+v", outcome)
	}
	if outcome.FinishReason != "" {
		t.Fatalf("finish reason must be unknown before decode: outcome=%+v", outcome)
	}
	if outcome.JSONErrorClass != "" || outcome.JSONErrorOffset != nil {
		t.Fatalf("JSON error telemetry must stay empty for a non-JSON failure: outcome=%+v", outcome)
	}
}

func TestGateFTelemetryOnResponseJSONInvalid(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `not json`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_json_invalid" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	if outcome.UsageAvailable || outcome.InputTokens != nil || outcome.OutputTokens != nil {
		t.Fatalf("usage must be unavailable -- json.Unmarshal itself failed: outcome=%+v", outcome)
	}
	if outcome.JSONErrorClass != "syntax_error" {
		t.Fatalf("expected json_error_class=syntax_error for a non-JSON body, got %q", outcome.JSONErrorClass)
	}
	if outcome.JSONErrorOffset == nil || *outcome.JSONErrorOffset <= 0 {
		t.Fatalf("expected a positive json_error_offset, got %+v", outcome.JSONErrorOffset)
	}
	if outcome.StartsWithJSONObject == nil || *outcome.StartsWithJSONObject {
		t.Fatalf("expected starts_with_json_object=false, got %+v", outcome.StartsWithJSONObject)
	}
}

func TestGateFTelemetryOnSuccessCarriesUsageAndStatus(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":50,"output_tokens":5}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	outcome := raw.ProviderOutcome
	if outcome.FinishReason != "completed" {
		t.Fatalf("expected finish_reason=completed (no incomplete_details to combine), got %q", outcome.FinishReason)
	}
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 50 || outcome.OutputTokens == nil || *outcome.OutputTokens != 5 {
		t.Fatalf("expected usage telemetry mirroring RawResponse: outcome=%+v", outcome)
	}
	if outcome.ResponseContentBytes == nil || *outcome.ResponseContentBytes <= 0 {
		t.Fatalf("expected a positive response content byte length, got %+v", outcome.ResponseContentBytes)
	}
}

// TestGateFJSONErrorOffsetMatchesStandardLibrary is the dedicated
// offset/class capture test: it independently reproduces the same
// json.Unmarshal call the adapter makes and asserts the adapter's captured
// offset is exactly the standard library's own SyntaxError.Offset.
func TestGateFJSONErrorOffsetMatchesStandardLibrary(t *testing.T) {
	body := `{"id":"r1","output":`
	var decoded responsesResponse
	independentErr := json.Unmarshal([]byte(body), &decoded)
	var expected *json.SyntaxError
	if !errors.As(independentErr, &expected) {
		t.Fatalf("test fixture must reproduce a json.SyntaxError, got %T: %v", independentErr, independentErr)
	}

	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/responses", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, dispatchErr := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(dispatchErr)
	if !ok || classified.Outcome.ErrorCode != "response_json_invalid" {
		t.Fatalf("error=%v classified=%+v", dispatchErr, classified)
	}
	outcome := classified.Outcome
	if outcome.JSONErrorOffset == nil || *outcome.JSONErrorOffset != expected.Offset {
		t.Fatalf("expected json_error_offset=%d (from encoding/json itself), got %+v", expected.Offset, outcome.JSONErrorOffset)
	}
}
