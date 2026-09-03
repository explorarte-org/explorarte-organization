package xai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/circuitbreaker"
)

const testToken = "xai-test-token-value-not-a-real-credential"

func credentialFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xai.token")
	if err := os.WriteFile(path, []byte(testToken), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(t *testing.T, endpoint string) Config {
	t.Helper()
	return Config{
		Enabled: true, EndpointURL: endpoint, CredentialFile: credentialFile(t),
		RequestTimeout: 5 * time.Second, FailureThreshold: 5, OpenDuration: time.Second,
		MaxResponseBytes: 1 << 20,
	}
}

// newTestAdapter points the adapter at an httptest server. The server speaks
// plain HTTP, so Validate's HTTPS rule is exercised separately in the config
// tests and bypassed here by constructing the Adapter directly.
func newTestAdapter(t *testing.T, handler http.HandlerFunc) (*Adapter, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config := testConfig(t, server.URL+"/v1/chat/completions")
	config.EndpointURL = server.URL + "/v1/chat/completions"
	adapter := &Adapter{
		config: config, client: server.Client(),
		descriptor: modelruntime.AdapterDescriptor{
			ProviderID: ProviderID, AdapterID: AdapterID, AdapterVersion: AdapterVersion,
			Transport: modelruntime.TransportHTTP, RequestSchemaVersion: RequestSchemaVersion,
			ResponseSchemaVersion: ResponseSchemaVersion,
		},
		breaker: circuitbreaker.New(config.FailureThreshold, config.OpenDuration),
		now:     time.Now,
	}
	return adapter, server
}

func canonicalRequest() modelruntime.CanonicalRequest {
	return modelruntime.CanonicalRequest{
		ProviderID: ProviderID, ProviderModelID: "test-model-synthetic",
		ProviderIdempotencyKey: "idem-1", MaxOutputTokens: 4096,
		RenderedContext: []byte("review this design"),
	}
}

// ------------------------------------------------------------ config

func TestConfigRejectsUnsafeEndpointsAndCredentials(t *testing.T) {
	base := func() Config {
		return Config{
			Enabled: true, EndpointURL: "https://api.x.ai/v1/chat/completions",
			CredentialFile: "/var/lib/explorarte/xai.token", RequestTimeout: time.Minute,
			FailureThreshold: 5, OpenDuration: 30 * time.Second, MaxResponseBytes: 1 << 20,
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*Config){
		"plain http":             func(c *Config) { c.EndpointURL = "http://api.x.ai/v1/chat/completions" },
		"userinfo":               func(c *Config) { c.EndpointURL = "https://user:pw@api.x.ai/v1/chat/completions" },
		"query string":           func(c *Config) { c.EndpointURL = "https://api.x.ai/v1/chat/completions?key=abc" },
		"fragment":               func(c *Config) { c.EndpointURL = "https://api.x.ai/v1/chat/completions#f" },
		"wrong path":             func(c *Config) { c.EndpointURL = "https://api.x.ai/v1/messages" },
		"path suffix smuggling":  func(c *Config) { c.EndpointURL = "https://evil.example/proxy/v1/chat/completions" },
		"empty host":             func(c *Config) { c.EndpointURL = "https:///v1/chat/completions" },
		"relative credential":    func(c *Config) { c.CredentialFile = "secrets/xai.token" },
		"empty credential":       func(c *Config) { c.CredentialFile = "" },
		"timeout too small":      func(c *Config) { c.RequestTimeout = time.Millisecond },
		"timeout too large":      func(c *Config) { c.RequestTimeout = time.Hour },
		"response cap too small": func(c *Config) { c.MaxResponseBytes = 16 },
		"threshold out of range": func(c *Config) { c.FailureThreshold = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := base()
			mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// An unconfigured deployment must produce no adapter at all rather than a
// half-wired one. This is the GROK_REVIEW_UNAVAILABLE=provider_not_configured
// state at the construction boundary.
func TestDisabledConfigYieldsNoAdapter(t *testing.T) {
	adapter, err := New(Config{Enabled: false, RequestTimeout: time.Minute, FailureThreshold: 5, OpenDuration: time.Second, MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatalf("disabled config errored: %v", err)
	}
	if adapter != nil {
		t.Fatal("disabled config produced an adapter")
	}
}

func TestLoadConfigUsesTheSharedEnvNamingAndDefaultsToDisabled(t *testing.T) {
	empty, err := LoadConfig(func(string) (string, bool) { return "", false }, 1<<20)
	if err != nil {
		t.Fatalf("empty environment errored: %v", err)
	}
	if empty.Enabled {
		t.Fatal("xai defaulted to enabled")
	}
	env := map[string]string{
		"ORG_MODEL_PROVIDER_XAI_ENABLED":         "true",
		"ORG_MODEL_PROVIDER_XAI_ENDPOINT_URL":    "https://api.x.ai/v1/chat/completions",
		"ORG_MODEL_PROVIDER_XAI_CREDENTIAL_FILE": "/var/lib/explorarte/program-v2/secrets/xai.token",
		"ORG_MODEL_PROVIDER_XAI_REQUEST_TIMEOUT": "90s",
	}
	config, err := LoadConfig(func(key string) (string, bool) { v, ok := env[key]; return v, ok }, 1<<20)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !config.Enabled || config.RequestTimeout != 90*time.Second {
		t.Fatalf("config=%+v", config)
	}
}

// ------------------------------------------------------------ request shape

func TestRequestUsesMaxCompletionTokensAndNeverMaxTokens(t *testing.T) {
	body, err := encodeRequest(canonicalRequest())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, deprecated := payload["max_tokens"]; deprecated {
		t.Fatal("adapter sent xAI's deprecated max_tokens field")
	}
	if payload["max_completion_tokens"] != float64(4096) {
		t.Fatalf("max_completion_tokens=%v", payload["max_completion_tokens"])
	}
	// Streaming is required, not optional. Without it the transport's
	// ResponseHeaderTimeout waits for the first byte of a completion that
	// sends none until it is finished, so it races the model's thinking time
	// -- which is exactly how AUTONOMY-SMOKE-001's adversarial review was cut
	// at 90 seconds with nothing billed and no way to tell whether the
	// provider had processed it.
	if payload["stream"] != true {
		t.Fatalf("stream=%v: a reasoning model must be streamed or its thinking time races the header timeout", payload["stream"])
	}
}

// A routing policy carrying an effort this provider does not accept must fail
// before the call, not after a wasted 400.
func TestUnsupportedReasoningEffortIsRejectedBeforeTheRequest(t *testing.T) {
	for _, effort := range []string{"xhigh", "maximum", "HIGH", "1"} {
		request := canonicalRequest()
		request.ReasoningEffort = effort
		if _, err := encodeRequest(request); !errors.Is(err, modelruntime.ErrInvalidRequest) {
			t.Fatalf("effort %q was not rejected: %v", effort, err)
		}
	}
	for _, effort := range []string{"", "none", "low", "medium", "high"} {
		request := canonicalRequest()
		request.ReasoningEffort = effort
		if _, err := encodeRequest(request); err != nil {
			t.Fatalf("effort %q rejected: %v", effort, err)
		}
	}
}

func TestStructuredOutputIsSentAsStrictJSONSchema(t *testing.T) {
	request := canonicalRequest()
	request.OutputMode = modelruntime.OutputJSON
	request.OutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["verdict"],"properties":{"verdict":{"type":"string","enum":["accept"]}}}`)
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Strict bool            `json:"strict"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ResponseFormat.Type != "json_schema" || !payload.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("response_format=%+v", payload.ResponseFormat)
	}
}

func TestEncodeRejectsForeignProviderAndEmptyBudget(t *testing.T) {
	for name, mutate := range map[string]func(*modelruntime.CanonicalRequest){
		"foreign provider": func(r *modelruntime.CanonicalRequest) { r.ProviderID = "deepseek" },
		"no model":         func(r *modelruntime.CanonicalRequest) { r.ProviderModelID = "" },
		"no budget":        func(r *modelruntime.CanonicalRequest) { r.MaxOutputTokens = 0 },
		"no context":       func(r *modelruntime.CanonicalRequest) { r.RenderedContext = nil },
	} {
		t.Run(name, func(t *testing.T) {
			request := canonicalRequest()
			mutate(&request)
			if _, err := encodeRequest(request); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

// ------------------------------------------------------------ responses

func chatBody(content string) string {
	return `{"id":"resp-1","choices":[{"message":{"content":` + strconv.Quote(content) + `},"finish_reason":"stop"}],
	 "usage":{"prompt_tokens":120,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":100}}}`
}

func TestDispatchParsesContentUsageAndPromptCache(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("authorization header=%q", got)
		}
		w.Header().Set("x-request-id", "req-abc")
		w.Write([]byte(chatBody(`{"verdict":"accept"}`)))
	})
	raw, err := adapter.Dispatch(context.Background(), canonicalRequest())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if string(raw.Content) != `{"verdict":"accept"}` {
		t.Fatalf("content=%q", raw.Content)
	}
	if raw.ProviderRequestID != "req-abc" || !raw.ProviderReported {
		t.Fatalf("provenance=%+v", raw)
	}
	if raw.InputTokens != 120 || raw.OutputTokens != 40 {
		t.Fatalf("usage=%d/%d", raw.InputTokens, raw.OutputTokens)
	}
	if raw.PromptCacheHitTokens == nil || *raw.PromptCacheHitTokens != 100 {
		t.Fatalf("cache hit=%v", raw.PromptCacheHitTokens)
	}
	if raw.PromptCacheMissTokens == nil || *raw.PromptCacheMissTokens != 20 {
		t.Fatalf("cache miss=%v", raw.PromptCacheMissTokens)
	}
	// Hidden reasoning must never be populated by this adapter.
	if len(raw.HiddenReasoning) != 0 {
		t.Fatal("adapter surfaced hidden reasoning")
	}
}

// Zero and "not reported" are different facts, and an incoherent split is not
// silently normalized into a plausible one.
func TestPromptCacheIsOnlyReportedWhenCoherent(t *testing.T) {
	if _, _, reported := promptCacheSplit(chatUsage{PromptTokens: 100}); reported {
		t.Fatal("absent details reported as a cache split")
	}
	if _, _, reported := promptCacheSplit(chatUsage{PromptTokens: 10, PromptTokensDetail: &promptTokensDetails{CachedTokens: 50}}); reported {
		t.Fatal("cached tokens exceeding the prompt total were reported")
	}
	hit, miss, reported := promptCacheSplit(chatUsage{PromptTokens: 10, PromptTokensDetail: &promptTokensDetails{CachedTokens: 0}})
	if !reported || hit != 0 || miss != 10 {
		t.Fatalf("explicit zero not reported: %d/%d/%v", hit, miss, reported)
	}
}

func TestTruncatedEmptyResponseIsAFailureNotAnEmptyReview(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":null},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":4096}}`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	if err == nil {
		t.Fatal("a budget-exhausted empty response was reported as success")
	}
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.ErrorCode != "response_truncated_empty" {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderErrorEnvelopeIsClassified(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","code":"rate_limited"}}`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) {
		t.Fatalf("err=%v", err)
	}
	if adapterErr.Outcome.ErrorClass != "rate_limit_error" || adapterErr.Outcome.ErrorCode != "rate_limited" {
		t.Fatalf("outcome=%+v", adapterErr.Outcome)
	}
	if !adapterErr.Outcome.Retryable || adapterErr.Outcome.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("outcome=%+v", adapterErr.Outcome)
	}
	if adapterErr.Outcome.OutcomeClassification != modelruntime.ProviderOutcomeRejected {
		t.Fatalf("classification=%v", adapterErr.Outcome.OutcomeClassification)
	}
}

// A 4xx that is not retryable must not be reported as ambiguous either: the
// request definitively reached the provider and was refused.
func TestNonRetryableRejectionIsDefinite(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"schema_unsupported"}}`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.Retryable {
		t.Fatalf("400 was marked retryable: %v", err)
	}
	if adapterErr.Phase != modelruntime.AdapterFailureResponseReceived {
		t.Fatalf("phase=%v", adapterErr.Phase)
	}
}

// A transport failure cannot prove whether the provider accepted the request,
// so it stays ambiguous. Reporting it as a clean failure is what produces
// duplicate provider calls on the retry.
func TestTransportFailureStaysAmbiguous(t *testing.T) {
	adapter, server := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {})
	server.Close()
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) {
		t.Fatalf("err=%v", err)
	}
	if adapterErr.Phase != modelruntime.AdapterFailureAmbiguous ||
		adapterErr.Outcome.OutcomeClassification != modelruntime.ProviderOutcomeAmbiguous {
		t.Fatalf("outcome=%+v", adapterErr.Outcome)
	}
	if !adapterErr.Outcome.Retryable {
		t.Fatal("ambiguous outcome was not retryable")
	}
}

func TestOversizedResponseIsBounded(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":"` + strings.Repeat("a", 4096) + `"},"finish_reason":"stop"}]}`))
	})
	adapter.config.MaxResponseBytes = 512
	if _, err := adapter.Dispatch(context.Background(), canonicalRequest()); err == nil {
		t.Fatal("response beyond the cap was accepted")
	}
}

// The credential must not reach any surface that becomes durable: not the
// outcome, not the error text, not the descriptor.
func TestCredentialNeverAppearsInErrorsOrDescriptor(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"type":"authentication_error","code":"invalid_api_key"}}`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatal("token leaked into the error text")
	}
	descriptor, _ := json.Marshal(adapter.Descriptor())
	if strings.Contains(string(descriptor), testToken) || strings.Contains(string(descriptor), adapter.config.CredentialFile) {
		t.Fatal("credential leaked into the descriptor")
	}
	var adapterErr *modelruntime.AdapterError
	if errors.As(err, &adapterErr) {
		outcome, _ := json.Marshal(adapterErr.Outcome)
		if strings.Contains(string(outcome), testToken) {
			t.Fatal("token leaked into the durable outcome")
		}
	}
}

func TestMissingCredentialFailsBeforeAnyRequest(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("provider was contacted despite an unreadable credential")
	})
	adapter.config.CredentialFile = filepath.Join(t.TempDir(), "absent.token")
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Phase != modelruntime.AdapterFailureBeforeRequest {
		t.Fatalf("err=%v", err)
	}
	if adapterErr.Outcome.OutcomeClassification != modelruntime.ProviderOutcomeNotSent {
		t.Fatalf("classification=%v", adapterErr.Outcome.OutcomeClassification)
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/v1/chat/completions", http.StatusTemporaryRedirect)
	})
	adapter.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("xai redirects are forbidden")
	}
	if _, err := adapter.Dispatch(context.Background(), canonicalRequest()); err == nil {
		t.Fatal("a redirect was followed")
	}
}

func TestPreflightRefusesForeignProviderAndExpiredDeadline(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {})
	if err := adapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: "deepseek", ProviderModelID: "m", Deadline: time.Now().Add(time.Minute),
	}); err == nil {
		t.Fatal("preflight accepted another provider's request")
	}
	if err := adapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: ProviderID, ProviderModelID: "m", Deadline: time.Now().Add(-time.Minute),
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline: %v", err)
	}
	if err := adapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: ProviderID, ProviderModelID: "m", Deadline: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("valid preflight rejected: %v", err)
	}
}

func TestProviderIDIsStable(t *testing.T) {
	if ProviderID != "xai" {
		t.Fatalf("ProviderID=%q -- canonical routing and the egress scope both key on this", ProviderID)
	}
}

// --- Gate F: Provider Failure Telemetry ------------------------------------
//
// Mirrors internal/modelruntime/adapter/deepseek's Gate F test suite: each
// case asserts both what gets populated and what stays NULL/false when the
// corresponding fact was never knowable at that point in the call.

func TestGateFTelemetryOnTruncatedEmpty(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":null},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":0}}`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.ErrorCode != "response_truncated_empty" {
		t.Fatalf("err=%v", err)
	}
	outcome := adapterErr.Outcome
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 10 || outcome.OutputTokens == nil || *outcome.OutputTokens != 0 {
		t.Fatalf("expected usage recovered from the decoded envelope: outcome=%+v", outcome)
	}
	if outcome.FinishReason != "length" {
		t.Fatalf("expected finish_reason=length, got %q", outcome.FinishReason)
	}
	if outcome.ResponseFormat != "text" || outcome.MaxOutputTokens == nil || *outcome.MaxOutputTokens != 4096 {
		t.Fatalf("expected request-shaping telemetry from the CanonicalRequest: outcome=%+v", outcome)
	}
	if outcome.RequestDuration == nil || *outcome.RequestDuration < 0 {
		t.Fatalf("expected a non-negative request duration, got %+v", outcome.RequestDuration)
	}
}

func TestGateFTelemetryIncludesReasoningTokensInBilledOutput(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"r","choices":[{"message":{"content":null},"finish_reason":"length"}],"usage":{"prompt_tokens":208,"completion_tokens":10,"completion_tokens_details":{"reasoning_tokens":1036}}}`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.ErrorCode != "response_truncated_empty" {
		t.Fatalf("err=%v", err)
	}
	outcome := adapterErr.Outcome
	// billedOutputTokens folds reasoning into the outcome-level telemetry too,
	// not just the success-path RawResponse -- the same undercounting risk
	// documented on billedOutputTokens applies to a failed call's Gate F row.
	if outcome.OutputTokens == nil || *outcome.OutputTokens != 1046 {
		t.Fatalf("expected billed output tokens (visible+reasoning)=1046, got %+v", outcome.OutputTokens)
	}
}

func TestGateFTelemetryOnResponseContentInvalid(t *testing.T) {
	body := `{"id":"r1","choices":[{"finish_reason":"stop","message":{"content":42}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.ErrorCode != "response_content_invalid" {
		t.Fatalf("err=%v", err)
	}
	outcome := adapterErr.Outcome
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
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	})
	_, err := adapter.Dispatch(context.Background(), canonicalRequest())
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.ErrorCode != "response_json_invalid" {
		t.Fatalf("err=%v", err)
	}
	outcome := adapterErr.Outcome
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

func TestGateFTelemetryOnSuccessCarriesUsageAndFinishReason(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-gatef")
		w.Write([]byte(chatBody(`{"verdict":"accept"}`)))
	})
	raw, err := adapter.Dispatch(context.Background(), canonicalRequest())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	outcome := raw.ProviderOutcome
	if outcome.FinishReason != "stop" {
		t.Fatalf("expected finish_reason=stop, got %q", outcome.FinishReason)
	}
	if !outcome.UsageAvailable || outcome.InputTokens == nil || *outcome.InputTokens != 120 || outcome.OutputTokens == nil || *outcome.OutputTokens != 40 {
		t.Fatalf("expected usage telemetry mirroring RawResponse: outcome=%+v", outcome)
	}
	if outcome.CacheHitTokens == nil || *outcome.CacheHitTokens != 100 || outcome.CacheMissTokens == nil || *outcome.CacheMissTokens != 20 {
		t.Fatalf("expected cache split telemetry: outcome=%+v", outcome)
	}
	if outcome.ResponseContentBytes == nil || *outcome.ResponseContentBytes <= 0 {
		t.Fatalf("expected a positive response content byte length, got %+v", outcome.ResponseContentBytes)
	}
}
