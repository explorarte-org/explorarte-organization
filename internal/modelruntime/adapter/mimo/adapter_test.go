package mimo

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
		ProviderID: ProviderID, ProviderModelID: "mimo-v2.5",
		ProviderIdempotencyKey: modelruntime.SHA256Bytes([]byte("provider-request")),
		ContextSnapshotID:      7, ContextRenderedHash: modelruntime.SHA256Bytes([]byte("hello")),
		RenderedContext: []byte("hello"), OutputMode: modelruntime.OutputText,
		MaxOutputTokens: 12000, ThinkingMode: modelruntime.ThinkingOpaque,
		Deadline: deadline,
	}
}

func TestConfigRequiresSecureExplicitEndpointAndCredentialReference(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	for name, endpoint := range map[string]string{
		"plain HTTP": "http://token-plan-sgp.xiaomimimo.com/v1/chat/completions",
		"userinfo":   "https://user@token-plan-sgp.xiaomimimo.com/v1/chat/completions",
		"query":      "https://token-plan-sgp.xiaomimimo.com/v1/chat/completions?x=1",
		"fragment":   "https://token-plan-sgp.xiaomimimo.com/v1/chat/completions#x",
		"wrong path": "https://token-plan-sgp.xiaomimimo.com/v1/responses",
	} {
		t.Run(name, func(t *testing.T) {
			if err := adapterConfig(endpoint, credential).Validate(); err == nil {
				t.Fatal("unsafe endpoint unexpectedly accepted")
			}
		})
	}
	// Unlike DeepSeek, MiMo's base URL is configurable and carries a "/v1"
	// prefix (audit section B) -- only the /chat/completions suffix is
	// fixed, so both shapes below must validate.
	for name, endpoint := range map[string]string{
		"documented base URL": "https://token-plan-sgp.xiaomimimo.com/v1/chat/completions",
		"no prefix":            "https://api.example.com/chat/completions",
	} {
		t.Run(name, func(t *testing.T) {
			if err := adapterConfig(endpoint, credential).Validate(); err != nil {
				t.Fatalf("expected valid endpoint to be accepted: %v", err)
			}
		})
	}
	cfg := adapterConfig("https://token-plan-sgp.xiaomimimo.com/v1/chat/completions", "relative-token")
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative credential path unexpectedly accepted")
	}
}

func TestDispatchSendsBoundedCanonicalRequestAndNormalizesResponse(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	var called atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		// MiMo's documented auth header is "api-key", never Authorization: Bearer.
		if got := r.Header.Get("api-key"); got != "test-provider-token" {
			t.Errorf("api-key header=%q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("must not send Authorization header, got %q", got)
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
		if payload["model"] != "mimo-v2.5" {
			t.Errorf("payload=%s", body)
		}
		if payload["max_completion_tokens"] != float64(12000) {
			t.Errorf("max_completion_tokens=%v want 12000: payload=%s", payload["max_completion_tokens"], body)
		}
		if _, present := payload["max_tokens"]; present {
			t.Errorf("mimo must never send max_tokens: payload=%s", body)
		}
		thinking, ok := payload["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Errorf("expected thinking:{type:enabled} by default, got %v: payload=%s", payload["thinking"], body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"mimo-resp-1","choices":[{"finish_reason":"stop","message":{"content":"ok","reasoning_content":"secret chain of thought"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)
	}))
	defer server.Close()

	cfg := adapterConfig(server.URL+"/v1/chat/completions", credential)
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
	if string(response.Content) != "ok" || response.InputTokens != 12 || response.OutputTokens != 3 || response.ProviderRequestID != "mimo-resp-1" {
		t.Fatalf("response=%+v", response)
	}
	if response.ProviderOutcome.OutcomeClassification != modelruntime.ProviderOutcomeResponseReceived || response.ProviderOutcome.HTTPStatus != http.StatusOK || len(response.ProviderOutcome.ResponseHash) != 64 {
		t.Fatalf("outcome=%+v", response.ProviderOutcome)
	}
}

// TestDispatchDiscardsHiddenReasoningBeforeNormalization proves
// reasoning_content is routed onto RawResponse.HiddenReasoning (never onto
// Content), and that Normalizer's existing, generic "raw.HiddenReasoning =
// nil" step (internal/modelruntime/normalizer.go) is what ultimately
// discards it -- this adapter's own job is only to route it there, not to
// discard it itself.
func TestDispatchDiscardsHiddenReasoningBeforeNormalization(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"stop","message":{"content":"final answer","reasoning_content":"must never be persisted"}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if string(response.Content) != "final answer" {
		t.Fatalf("content=%q", response.Content)
	}
	if string(response.HiddenReasoning) != "must never be persisted" {
		t.Fatalf("expected reasoning_content routed onto HiddenReasoning, got %q", response.HiddenReasoning)
	}
	invocation := modelruntime.Invocation{ID: 1, OutputMode: modelruntime.OutputText}
	normalized, err := (modelruntime.Normalizer{MaxResponseBytes: 1 << 20, MaxToolIntents: 8}).Normalize(invocation, 2, response)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Result.TextOutput != "final answer" {
		t.Fatalf("normalized text=%q", normalized.Result.TextOutput)
	}
}

func TestEncodeRequestUsesMaxCompletionTokensAndDefaultThinking(t *testing.T) {
	request := validRequest(time.Now().Add(time.Minute))
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, present := payload["max_tokens"]; present {
		t.Errorf("must omit max_tokens: payload=%s", body)
	}
	if payload["max_completion_tokens"] != float64(12000) {
		t.Errorf("max_completion_tokens=%v want 12000: payload=%s", payload["max_completion_tokens"], body)
	}
	thinking, ok := payload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Errorf("expected thinking:{type:enabled}, got %v", payload["thinking"])
	}
}

// TestJSONObjectModeInstructionForbidsMarkdownFences asserts the exact
// anti-markdown-fence instruction text is present in the rendered prompt --
// the single most important behavioral requirement per the audit (real,
// confirmed: mimo-v2.5 wraps JSON output in a ```json fence without this
// explicit instruction, even in json_object mode).
func TestJSONObjectModeInstructionForbidsMarkdownFences(t *testing.T) {
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
	messages, _ := payload["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected exactly one message, got %d", len(messages))
	}
	content, _ := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(content, "Do not wrap the JSON object in a markdown code fence") {
		t.Fatalf("expected explicit anti-markdown-fence instruction in prompt: content=%s", content)
	}
	if !strings.Contains(content, "JSON Schema") {
		t.Fatalf("expected the schema contract to be appended to the prompt: content=%s", content)
	}
	responseFormat, ok := payload["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("response_format=%v, want {type: json_object}", payload["response_format"])
	}
}

// TestDecodeContentStripsWrappingMarkdownFence covers the defensive,
// belt-and-suspenders fence-stripping this adapter adds IN ADDITION to the
// prompt instruction (see decodeContent's doc comment for why both exist).
// This is the only way to exercise the fenced-response case offline, since
// a live call proving the prompt instruction alone prevents fencing cannot
// be made in this task.
func TestDecodeContentStripsWrappingMarkdownFence(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "json-tagged fence", input: "```json\n{\"ok\":true}\n```", want: `{"ok":true}`},
		{name: "bare fence", input: "```\n{\"ok\":true}\n```", want: `{"ok":true}`},
		{name: "no fence unaffected", input: `{"ok":true}`, want: `{"ok":true}`},
		{name: "fence mid text left alone", input: "prose ``` inline ``` more", want: "prose ``` inline ``` more"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeContent(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("decodeContent(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestDispatchClassifiesAbortFinishReasonWithDistinctCode(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"abort","message":{"content":"partial"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Phase != modelruntime.AdapterFailureResponseReceived || classified.Outcome.ErrorCode != "response_aborted" || classified.Outcome.Retryable {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	if !response.ProviderReported || response.InputTokens != 10 {
		t.Fatalf("expected recovered usage on abort: response=%+v", response)
	}
}

func TestDispatchRejectsTruncatedEmptyResponseAsFailure(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"length","message":{"content":null}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
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
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	if err != nil || string(response.Content) != "partial but usable" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestDispatchClassifiesProviderRejectionWithoutLeakingMessage(t *testing.T) {
	credential := writeCredential(t, "super-secret-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"sensitive provider message"}}`)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
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
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
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
	cfg := adapterConfig(server.URL+"/v1/chat/completions", credential)
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
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with provider details")
	})}
	adapter, err := newAdapter(adapterConfig("https://token-plan-sgp.xiaomimimo.com/v1/chat/completions", credential), client, func() time.Time { return now })
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
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Phase != modelruntime.AdapterFailureAmbiguous {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
}

// TestDispatchParsesCacheTokens covers the cache-token contract: MiMo only
// reports a hit count (usage.prompt_tokens_details.cached_tokens); miss is
// computed as prompt_tokens - cached_tokens (documented choice, see
// cacheTokens' doc comment). Omitted fields stay nil, never zero.
func TestDispatchParsesCacheTokens(t *testing.T) {
	t.Run("cached tokens present, miss computed", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"stop","message":{"content":"ok"}}],"usage":{"prompt_tokens":256,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":192},"completion_tokens_details":{"reasoning_tokens":5}}}`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if response.PromptCacheHitTokens == nil || *response.PromptCacheHitTokens != 192 {
			t.Fatalf("cache hit tokens not parsed: response=%+v", response)
		}
		if response.PromptCacheMissTokens == nil || *response.PromptCacheMissTokens != 64 {
			t.Fatalf("cache miss tokens not computed correctly: response=%+v", response)
		}
	})
	t.Run("prompt_tokens_details omitted stays nil, never zero", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"r1","choices":[{"finish_reason":"stop","message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		response, err := adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		if response.PromptCacheHitTokens != nil || response.PromptCacheMissTokens != nil {
			t.Fatalf("expected nil cache tokens when the provider omits prompt_tokens_details, got %+v", response)
		}
	})
}

// TestDispatchPreservesUsageOnBusinessFailuresAfterDecodeSucceeded is the
// usage-survives-business-failure contract, built into this adapter from
// day one (not a second-round patch): response_truncated_empty,
// response_aborted, and response_choice_count_invalid all fire AFTER
// json.Unmarshal of the outer envelope already succeeded, so decoded.Usage
// must still be threaded onto the returned RawResponse even though an
// AdapterError is also returned.
func TestDispatchPreservesUsageOnBusinessFailuresAfterDecodeSucceeded(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{name: "truncated empty", body: `{"id":"r1","choices":[{"finish_reason":"length","message":{"content":null}}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`, code: "response_truncated_empty"},
		{name: "aborted", body: `{"id":"r1","choices":[{"finish_reason":"abort","message":{"content":"x"}}],"usage":{"prompt_tokens":11,"completion_tokens":2}}`, code: "response_aborted"},
		{name: "choice count invalid", body: `{"id":"r1","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":0}}`, code: "response_choice_count_invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			credential := writeCredential(t, "test-provider-token")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
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

func TestDispatchHasNoRecoverableUsageWhenDecodeNeverSucceeded(t *testing.T) {
	t.Run("json invalid", func(t *testing.T) {
		credential := writeCredential(t, "test-provider-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `not json`)
		}))
		defer server.Close()
		adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
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
}

// --- Gate F: Provider Failure Telemetry ------------------------------------

func TestGateFTelemetryOnResponseReadFailed(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
	}))
	defer server.Close()
	cfg := adapterConfig(server.URL+"/v1/chat/completions", credential)
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
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_json_invalid" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	if outcome.JSONErrorClass != "syntax_error" {
		t.Fatalf("expected json_error_class=syntax_error, got %q", outcome.JSONErrorClass)
	}
	if outcome.JSONErrorOffset == nil || *outcome.JSONErrorOffset <= 0 {
		t.Fatalf("expected a positive json_error_offset, got %+v", outcome.JSONErrorOffset)
	}
}

func TestGateFTelemetryOnAbort(t *testing.T) {
	credential := writeCredential(t, "test-provider-token")
	body := `{"id":"r1","choices":[{"finish_reason":"abort","message":{"content":"x"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	adapter, err := newAdapter(adapterConfig(server.URL+"/v1/chat/completions", credential), server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), validRequest(time.Now().Add(time.Minute)))
	classified, ok := modelruntime.AsAdapterError(err)
	if !ok || classified.Outcome.ErrorCode != "response_aborted" {
		t.Fatalf("error=%v classified=%+v", err, classified)
	}
	outcome := classified.Outcome
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 10 {
		t.Fatalf("expected usage recovered from the decoded envelope: outcome=%+v", outcome)
	}
	if outcome.FinishReason != "abort" {
		t.Fatalf("expected finish_reason=abort, got %q", outcome.FinishReason)
	}
	if validateErr := outcome.Validate(); validateErr != nil {
		t.Fatal(validateErr)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
