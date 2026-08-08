package deepseek

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
		DispatchActorRoleID: "ingenieria_ia/code-runner", SubjectRoleID: "ingenieria_ia/qa",
		ModelProfileID: "department.worker", ModelProfileVersionID: 6,
		ProviderID: ProviderID, ProviderModelID: "deepseek-v4-flash",
		ProviderIdempotencyKey: modelruntime.SHA256Bytes([]byte("provider-request")),
		ContextSnapshotID:      7, ContextRenderedHash: modelruntime.SHA256Bytes([]byte("hello")),
		RenderedContext: []byte("hello"), OutputMode: modelruntime.OutputText,
		MaxOutputTokens: 64, ThinkingMode: modelruntime.ThinkingOpaque,
		Deadline: deadline,
	}
}

func TestConfigRequiresSecureExplicitEndpointAndCredentialReference(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	for name, endpoint := range map[string]string{
		"plain HTTP":      "http://api.deepseek.com/chat/completions",
		"userinfo":        "https://user@api.deepseek.com/chat/completions",
		"query":           "https://api.deepseek.com/chat/completions?x=1",
		"fragment":        "https://api.deepseek.com/chat/completions#x",
		"v1 prefix wrong": "https://api.deepseek.com/v1/chat/completions",
		"wrong path":      "https://api.deepseek.com/responses",
	} {
		t.Run(name, func(t *testing.T) {
			if err := adapterConfig(endpoint, credential).Validate(); err == nil {
				t.Fatal("unsafe endpoint unexpectedly accepted")
			}
		})
	}
	cfg := adapterConfig("https://api.deepseek.com/chat/completions", "relative-token")
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative credential path unexpectedly accepted")
	}
	if err := adapterConfig("https://api.deepseek.com/chat/completions", credential).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchSendsBoundedCanonicalRequestAndNormalizesResponse(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	var called atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-provider-token" {
			t.Errorf("authorization header=%q", got)
		}
		if got := r.Header.Get("X-Client-Request-Id"); len(got) != 64 {
			t.Errorf("client request ID=%q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var payload map[string]any
		if err = json.Unmarshal(body, &payload); err != nil {
			t.Error(err)
			return
		}
		if payload["model"] != "deepseek-v4-flash" || payload["stream"] != false {
			t.Errorf("payload=%s", body)
		}
		if _, present := payload["reasoning_effort"]; present {
			t.Errorf("non-reasoning request must omit reasoning_effort: payload=%s", body)
		}
		if payload["max_tokens"] != float64(64) {
			t.Errorf("max_tokens=%v want 64: payload=%s", payload["max_tokens"], body)
		}
		w.Header().Set("x-request-id", "provider-request-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fallback-id","choices":[{"message":{"content":"ok","tool_calls":[{"function":{"name":"inspect","arguments":{"read_only":true}}}]}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)
	}))
	defer server.Close()

	cfg := adapterConfig(server.URL+"/chat/completions", credential)
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
	if called.Load() != 1 {
		t.Fatalf("called=%d", called.Load())
	}
	if string(response.Content) != "ok" || response.InputTokens != 12 || response.OutputTokens != 3 || response.ProviderRequestID != "provider-request-1" {
		t.Fatalf("response=%+v", response)
	}
	if len(response.ToolIntents) != 1 || response.ToolIntents[0].Name != "inspect" {
		t.Fatalf("tool intents=%+v", response.ToolIntents)
	}
	if response.ProviderOutcome.OutcomeClassification != modelruntime.ProviderOutcomeResponseReceived || response.ProviderOutcome.HTTPStatus != http.StatusOK || len(response.ProviderOutcome.ResponseHash) != 64 {
		t.Fatalf("outcome=%+v", response.ProviderOutcome)
	}
}

func TestEncodeRequestUsesMaxTokensFieldMatchingReasoningEffort(t *testing.T) {
	reasoning := validRequest(time.Now().Add(time.Minute))
	reasoning.ReasoningEffort = "high"
	body, err := encodeRequest(reasoning)
	if err != nil {
		t.Fatal(err)
	}
	var reasoningPayload map[string]any
	if err = json.Unmarshal(body, &reasoningPayload); err != nil {
		t.Fatal(err)
	}
	if _, present := reasoningPayload["max_tokens"]; present {
		t.Errorf("reasoning-effort request must omit max_tokens: payload=%s", body)
	}
	if reasoningPayload["max_completion_tokens"] != float64(64) {
		t.Errorf("reasoning-effort request max_completion_tokens=%v want 64: payload=%s", reasoningPayload["max_completion_tokens"], body)
	}

	nonReasoning := validRequest(time.Now().Add(time.Minute))
	nonReasoning.ReasoningEffort = ""
	body, err = encodeRequest(nonReasoning)
	if err != nil {
		t.Fatal(err)
	}
	var nonReasoningPayload map[string]any
	if err = json.Unmarshal(body, &nonReasoningPayload); err != nil {
		t.Fatal(err)
	}
	if _, present := nonReasoningPayload["max_completion_tokens"]; present {
		t.Errorf("non-reasoning request must omit max_completion_tokens: payload=%s", body)
	}
	if nonReasoningPayload["max_tokens"] != float64(64) {
		t.Errorf("non-reasoning request max_tokens=%v want 64: payload=%s", nonReasoningPayload["max_tokens"], body)
	}
}

func TestDispatchUsesStructuredOutputWithoutPersistingSchemaContent(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"type":"json_schema"`) || !strings.Contains(string(body), `"strict":true`) {
			t.Errorf("body=%s", body)
		}
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest(time.Now().Add(time.Minute))
	request.OutputMode = modelruntime.OutputJSON
	request.OutputSchema = json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	response, err := adapter.Dispatch(context.Background(), request)
	if err != nil || string(response.Content) != `{"ok":true}` {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestDispatchClassifiesProviderRejectionWithoutLeakingMessage(t *testing.T) {
	credential := writeCredential(t, "super-secret-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "rejected-1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"sensitive provider message"}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Phase != modelruntime.AdapterFailureResponseReceived || classified.Outcome.HTTPStatus != http.StatusTooManyRequests || !classified.Outcome.Retryable || classified.Outcome.ErrorClass != "rate_limit_error" || classified.Outcome.ErrorCode != "rate_limit_exceeded" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	text := err.Error()
	if strings.Contains(text, "sensitive provider message") || strings.Contains(text, "super-secret-provider-token") {
		t.Fatalf("secret or provider message leaked: %s", text)
	}
}

func TestProviderErrorMetadataIsNormalizedBeforePersistence(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"secret message with spaces","code":{"nested":"do not persist"},"message":"sensitive"}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorClass != "provider" || classified.Outcome.ErrorCode != "http_error" {
		t.Fatalf("error=%v outcome=%+v", err, classified)
	}
	if validateErr := classified.Outcome.Validate(); validateErr != nil {
		t.Fatal(validateErr)
	}
}

func TestDispatchRejectsOversizedResponseAndRecordsOnlyBoundedHash(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
	}))
	defer server.Close()
	cfg := adapterConfig(server.URL+"/chat/completions", credential)
	cfg.MaxResponseBytes = 1024
	adapter, err := newAdapter(cfg, server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_read_failed" || len(classified.Outcome.ResponseHash) != 64 {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
}

func TestTransportFailureIsAmbiguousAndCircuitOpens(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with provider details")
	})}
	adapter, err := newAdapter(adapterConfig("https://api.deepseek.com/chat/completions", credential), client, func() time.Time { return now })
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
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
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
