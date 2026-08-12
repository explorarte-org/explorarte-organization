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

// TestDispatchUsesStructuredOutputWithoutPersistingSchemaContent confirms
// the request DeepSeek's real Chat Completions endpoint actually accepts:
// response_format={"type":"json_object"}, never the native json_schema/
// strict mode that endpoint does not support (confirmed live -- sending
// json_schema there returns invalid_request_error; that mode exists only
// under DeepSeek's separate beta Function Calling surface, deliberately
// not adopted here). The schema itself still reaches the model, as an
// explicit textual contract in the prompt -- schema conformance is
// enforced afterward by internal/modelruntime.Normalizer, generically,
// not requested as a provider-side guarantee this endpoint cannot make.
func TestDispatchUsesStructuredOutputWithoutPersistingSchemaContent(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"type":"json_object"`) {
			t.Errorf("expected json_object response_format (DeepSeek's real Chat Completions endpoint does not support json_schema/strict): body=%s", body)
		}
		if strings.Contains(string(body), `"type":"json_schema"`) || strings.Contains(string(body), `"strict":true`) {
			t.Errorf("must not send the unsupported json_schema/strict mode: body=%s", body)
		}
		if !strings.Contains(string(body), `JSON Schema`) {
			t.Errorf("expected the schema contract text to reach the model in the prompt: body=%s", body)
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

func TestDispatchRejectsTruncatedEmptyResponseAsFailure(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"length","message":{"content":null}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_truncated_empty" || classified.Outcome.Retryable {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
}

func TestDispatchAcceptsTruncatedResponseWithPartialContent(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"length","message":{"content":"partial but usable"}}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	if err != nil || string(response.Content) != "partial but usable" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestDispatchRejectsContentFilteredResponseAsFailure(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"content_filter","message":{"content":null}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_content_filtered" || classified.Outcome.Retryable {
		t.Fatalf("error=%v classified=%+v", err, classified)
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

func TestEncodeRequestUsesJSONObjectNotJSONSchema(t *testing.T) {
	request := validRequest(time.Now().Add(time.Minute))
	request.OutputMode = modelruntime.OutputJSON
	request.OutputSchema = json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	responseFormat, ok := payload["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("response_format=%v, want {type: json_object}", payload["response_format"])
	}
	if _, present := responseFormat["json_schema"]; present {
		t.Fatalf("response_format must not carry json_schema (unsupported by DeepSeek's Chat Completions endpoint): %v", responseFormat)
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected exactly one message, got %d", len(messages))
	}
	content, _ := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, "JSON Schema") || !strings.Contains(strings.ToLower(content), "json") {
		t.Fatalf("expected the schema contract to be appended to the prompt: content=%s", content)
	}
}

func TestEncodeRequestOmitsResponseFormatBodyWithoutSchema(t *testing.T) {
	request := validRequest(time.Now().Add(time.Minute))
	request.OutputMode = modelruntime.OutputJSON
	request.OutputSchema = nil
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	responseFormat, ok := payload["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("response_format=%v, want {type: json_object} even with no schema", payload["response_format"])
	}
}

// TestDispatchPreservesUsageOnBusinessFailuresAfterDecodeSucceeded is the
// P0 usage-survives-failure regression: response_truncated_empty and
// response_content_filtered both fire AFTER the adapter's own
// json.Unmarshal of the response body already succeeded, so decoded.Usage
// is known even though the call is being rejected. Before this change the
// adapter discarded it (returned a zero-value RawResponse{}), which is
// exactly what made these calls financially invisible upstream.
func TestDispatchPreservesUsageOnBusinessFailuresAfterDecodeSucceeded(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{name: "truncated empty", body: `{"id":"r1","choices":[{"finish_reason":"length","message":{"content":null}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`, code: "response_truncated_empty"},
		{name: "content filtered", body: `{"id":"r1","choices":[{"finish_reason":"content_filter","message":{"content":null}}],"usage":{"prompt_tokens":11,"completion_tokens":2}}`, code: "response_content_filtered"},
		{name: "choice count invalid", body: `{"id":"r1","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":0}}`, code: "response_choice_count_invalid"},
		{name: "tool call name missing", body: `{"id":"r1","choices":[{"message":{"content":null,"tool_calls":[{"function":{"name":"","arguments":{}}}]}}],"usage":{"prompt_tokens":8,"completion_tokens":1}}`, code: "tool_call_name_missing"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			credential := writeCredential(t, "test-provider-token")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			response, dispatchErr := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
			classified, ok := modelruntime.AsAdapterError(dispatchErr)
			if !ok || classified.Outcome.ErrorCode != test.code {
				t.Fatalf("error=%v classified=%+v", dispatchErr, classified)
			}
			if !response.ProviderReported {
				t.Fatalf("expected recovered usage to be marked provider-reported: response=%+v", response)
			}
		})
	}
}

// TestDispatchHasNoRecoverableUsageWhenDecodeNeverSucceeded covers the
// genuinely unrecoverable half of the same phase: response_read_failed and
// response_json_invalid both fire BEFORE any usage object was ever decoded,
// so RawResponse.ProviderReported must stay false -- there is nothing real
// to commit, only the conservative reservation estimate applies.
func TestDispatchHasNoRecoverableUsageWhenDecodeNeverSucceeded(t *testing.T) {
	t.Run("json invalid", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `not json`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		response, dispatchErr := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		classified, ok := modelruntime.AsAdapterError(dispatchErr)
		if !ok || classified.Outcome.ErrorCode != "response_json_invalid" {
			t.Fatalf("error=%v classified=%+v", dispatchErr, classified)
		}
		if response.ProviderReported {
			t.Fatalf("expected no recoverable usage: response=%+v", response)
		}
	})
	t.Run("read failed", func(t *testing.T) {
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
		response, dispatchErr := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		classified, ok := modelruntime.AsAdapterError(dispatchErr)
		if !ok || classified.Outcome.ErrorCode != "response_read_failed" {
			t.Fatalf("error=%v classified=%+v", dispatchErr, classified)
		}
		if response.ProviderReported {
			t.Fatalf("expected no recoverable usage: response=%+v", response)
		}
	})
}

// TestDispatchParsesCacheTokens covers point 5: all-present passes the
// invariant silently, a mismatch is only logged (never fails the call),
// and fields DeepSeek omits are stored as nil (NULL), never defaulted to
// zero.
func TestDispatchParsesCacheTokens(t *testing.T) {
	t.Run("all present and consistent", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"r1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4}}`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if response.PromptCacheHitTokens == nil || *response.PromptCacheHitTokens != 6 || response.PromptCacheMissTokens == nil || *response.PromptCacheMissTokens != 4 {
			t.Fatalf("cache tokens not parsed: response=%+v", response)
		}
	})
	t.Run("omitted fields stay nil, never zero", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"r1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if response.PromptCacheHitTokens != nil || response.PromptCacheMissTokens != nil {
			t.Fatalf("expected nil cache tokens when the provider omits them, got %+v", response)
		}
	})
	t.Run("mismatch is not fatal", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"r1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":1}}`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		// A mismatched split is logged (validateCacheTokens), not rejected --
		// the call must still succeed and the raw values are still stored.
		if response.PromptCacheHitTokens == nil || *response.PromptCacheHitTokens != 1 || response.PromptCacheMissTokens == nil || *response.PromptCacheMissTokens != 1 {
			t.Fatalf("mismatched cache tokens should still be stored as reported: response=%+v", response)
		}
	})
}

// --- Gate F: Provider Failure Telemetry ------------------------------------
//
// The tests below cover the four documented failure classes plus the
// JSON-decode-failure-specific offset/class capture. Each asserts both what
// gets populated and, just as importantly, what stays NULL/false when the
// corresponding fact was never knowable at that point in the call (e.g. no
// usage object exists before json.Unmarshal succeeds).

func TestGateFTelemetryOnResponseReadFailed(t *testing.T) {
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
	if !ok || classified.Outcome.ErrorCode != "response_read_failed" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	// The bounded reader still yields the truncated bytes it read even on
	// failure, so a byte length is knowable; nothing past that point (usage,
	// finish reason) ever got decoded.
	if outcome.ResponseContentBytes == nil || *outcome.ResponseContentBytes <= 0 {
		t.Fatalf("expected a positive response content byte length, got %+v", outcome.ResponseContentBytes)
	}
	if outcome.UsageAvailable || outcome.InputTokens != nil || outcome.OutputTokens != nil {
		t.Fatalf("usage must be unavailable before any decode was attempted: outcome=%+v", outcome)
	}
	if outcome.FinishReason != "" {
		t.Fatalf("finish reason must be unknown before decode: outcome=%+v", outcome)
	}
	if outcome.RequestDuration == nil || *outcome.RequestDuration < 0 {
		t.Fatalf("expected a non-negative request duration, got %+v", outcome.RequestDuration)
	}
	if outcome.ResponseFormat != "text" || outcome.MaxOutputTokens == nil || *outcome.MaxOutputTokens != 64 {
		t.Fatalf("expected request-shaping telemetry from the CanonicalRequest: outcome=%+v", outcome)
	}
	if outcome.JSONErrorClass != "" || outcome.JSONErrorOffset != nil {
		t.Fatalf("JSON error telemetry must stay empty for a non-JSON failure: outcome=%+v", outcome)
	}
}

func TestGateFTelemetryOnTruncatedEmpty(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	body := `{"id":"r1","choices":[{"finish_reason":"length","message":{"content":null}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_truncated_empty" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 10 || outcome.OutputTokens == nil || *outcome.OutputTokens != 0 {
		t.Fatalf("expected usage recovered from the decoded envelope: outcome=%+v", outcome)
	}
	if outcome.FinishReason != "length" {
		t.Fatalf("expected finish_reason=length, got %q", outcome.FinishReason)
	}
	if outcome.ResponseContentBytes == nil || *outcome.ResponseContentBytes != len(body) {
		t.Fatalf("expected response_content_bytes=%d, got %+v", len(body), outcome.ResponseContentBytes)
	}
	if outcome.ResponseFormat != "text" || outcome.MaxOutputTokens == nil || *outcome.MaxOutputTokens != 64 {
		t.Fatalf("expected request-shaping telemetry from the CanonicalRequest: outcome=%+v", outcome)
	}
	if outcome.RequestDuration == nil || *outcome.RequestDuration < 0 {
		t.Fatalf("expected a non-negative request duration, got %+v", outcome.RequestDuration)
	}
}

func TestGateFTelemetryOnResponseContentInvalid(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	// Content is a bare number: not a string, not an array of {type,text}
	// parts -- decodeContent fails, triggering response_content_invalid
	// *after* the envelope (including usage) was already decoded.
	body := `{"id":"r1","choices":[{"finish_reason":"stop","message":{"content":42}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_content_invalid" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 7 || outcome.OutputTokens == nil || *outcome.OutputTokens != 3 {
		t.Fatalf("expected usage recovered from the decoded envelope: outcome=%+v", outcome)
	}
	if outcome.FinishReason != "stop" {
		t.Fatalf("expected finish_reason=stop, got %q", outcome.FinishReason)
	}
	if outcome.JSONErrorClass != "" || outcome.JSONErrorOffset != nil {
		t.Fatalf("JSON error telemetry must stay empty -- the envelope itself decoded fine: outcome=%+v", outcome)
	}
}

func TestGateFTelemetryOnResponseJSONInvalid(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `not json`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
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
		t.Fatalf("expected starts_with_json_object=false for %q, got %+v", "not json", outcome.StartsWithJSONObject)
	}
	if outcome.EndsWithJSONObject == nil || *outcome.EndsWithJSONObject {
		t.Fatalf("expected ends_with_json_object=false for %q, got %+v", "not json", outcome.EndsWithJSONObject)
	}
}

// TestGateFJSONErrorOffsetMatchesStandardLibrary is the dedicated
// offset/class capture test: it independently reproduces the same
// json.Unmarshal call the adapter makes and asserts the adapter's captured
// offset is exactly the standard library's own SyntaxError.Offset, not an
// approximation.
func TestGateFJSONErrorOffsetMatchesStandardLibrary(t *testing.T) {
	body := `{"id":"r1","choices":`
	var decoded chatResponse
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
	adapter, err := newAdapter(adapterConfig(server.URL+"/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, dispatchErr := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(dispatchErr)
	if !ok || classified.Outcome.ErrorCode != "response_json_invalid" {
		t.Fatalf("error=%v classified=%+v", dispatchErr, classified)
	}
	outcome := classified.Outcome
	if outcome.JSONErrorClass != "syntax_error" {
		t.Fatalf("expected json_error_class=syntax_error, got %q", outcome.JSONErrorClass)
	}
	if outcome.JSONErrorOffset == nil || *outcome.JSONErrorOffset != expected.Offset {
		t.Fatalf("expected json_error_offset=%d (from encoding/json itself), got %+v", expected.Offset, outcome.JSONErrorOffset)
	}
	if outcome.StartsWithJSONObject == nil || !*outcome.StartsWithJSONObject {
		t.Fatalf("expected starts_with_json_object=true (body starts with '{'), got %+v", outcome.StartsWithJSONObject)
	}
	if outcome.EndsWithJSONObject == nil || *outcome.EndsWithJSONObject {
		t.Fatalf("expected ends_with_json_object=false (body does not end with '}'), got %+v", outcome.EndsWithJSONObject)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
