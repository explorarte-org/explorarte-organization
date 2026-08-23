package mimo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/secrets"
)

type Adapter struct {
	config     Config
	client     *http.Client
	descriptor modelruntime.AdapterDescriptor
	breaker    *circuitBreaker
	now        func() time.Time
}

// thinkingConfig is always sent as {"type":"enabled"} -- MiMo-V2.5 has
// reasoning enabled by default and it is deliberately never disabled by
// this adapter (see docs/reports/MIMO_V25_INTEGRATION_AUDIT.md section E):
// disabling it to save budget would be giving this challenger provider a
// configuration advantage DeepSeek does not get, which the owner explicitly
// ruled out ("no ventajas"). There is no Config field or env var to toggle
// this -- it is not meant to be tunable.
type thinkingConfig struct {
	Type string `json:"type"`
}

var alwaysEnabledThinking = thinkingConfig{Type: "enabled"}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	Thinking            thinkingConfig  `json:"thinking"`
	Tools               []chatTool      `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}
type chatToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}
type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type chatResponseMessage struct {
	Content json.RawMessage `json:"content"`
	// ReasoningContent must never be persisted, logged, or hashed into any
	// durable structure -- it is routed onto RawResponse.HiddenReasoning,
	// which internal/modelruntime.Normalizer already discards
	// unconditionally before assembling any durable result (see
	// normalizer.go: "raw.HiddenReasoning = nil"). DeepSeek's adapter has
	// no equivalent field today (confirmed by reading its chatResponseMessage
	// struct -- it has no hidden-reasoning field at all), so this is
	// MiMo-specific wiring, not a shared pattern being reused.
	ReasoningContent string         `json:"reasoning_content"`
	ToolCalls        []chatToolCall `json:"tool_calls"`
}

type chatUsage struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details"`
}

type promptTokensDetails struct {
	// CachedTokens is a pointer because an absent/omitted field must be
	// stored as NULL (unknown), never fabricated as zero -- same rule the
	// deepseek adapter already applies to its own cache fields.
	CachedTokens *int64 `json:"cached_tokens"`
}

type completionTokensDetails struct {
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

// cacheTokens extracts (hit, miss) from the provider's usage object.
//
// Unlike DeepSeek (which reports both prompt_cache_hit_tokens and
// prompt_cache_miss_tokens directly), MiMo's real, confirmed response shape
// (docs/reports/MIMO_V25_INTEGRATION_AUDIT.md section G) only reports the
// hit count via usage.prompt_tokens_details.cached_tokens -- there is no
// separate miss field. This adapter computes miss as
// prompt_tokens - cached_tokens whenever cached_tokens is known, mirroring
// the same "prompt_tokens splits into hit+miss" invariant DeepSeek's own
// adapter already validates (see validateCacheTokens there) -- a fair,
// evidence-grounded computation, not a fabricated number. Both stay nil
// when the provider omits prompt_tokens_details entirely.
func cacheTokens(usage chatUsage) (hit, miss *int64) {
	if usage.PromptTokensDetails == nil || usage.PromptTokensDetails.CachedTokens == nil {
		return nil, nil
	}
	hitValue := *usage.PromptTokensDetails.CachedTokens
	missValue := usage.PromptTokens - hitValue
	if missValue < 0 {
		// Defensive: never fabricate a negative miss count from an
		// inconsistent provider response. Log and leave miss unknown; the
		// hit count itself is still real and still stored.
		slog.Default().Warn("mimo usage cached_tokens exceeds prompt_tokens", "prompt_tokens", usage.PromptTokens, "cached_tokens", hitValue)
		return &hitValue, nil
	}
	return &hitValue, &missValue
}

type providerErrorEnvelope struct {
	Error struct {
		Type string `json:"type"`
		Code any    `json:"code"`
	} `json:"error"`
}

// failureTelemetry mirrors deepseek's failureTelemetry builder exactly
// (see internal/modelruntime/adapter/deepseek/adapter.go) -- same Gate F
// contract, same optional/additive fields, none of it ever prompt/
// completion/hidden-reasoning content.
type failureTelemetry struct {
	responseFormat       string
	maxOutputTokens      *int
	requestDuration      *time.Duration
	responseContentBytes *int
	finishReason         string
	usageAvailable       bool
	inputTokens          *int64
	outputTokens         *int64
	cacheHitTokens       *int64
	cacheMissTokens      *int64
	jsonErrorClass       string
	jsonErrorOffset      *int64
	startsWithJSONObject *bool
	endsWithJSONObject   *bool
}

func requestTelemetry(request modelruntime.CanonicalRequest) failureTelemetry {
	format := "text"
	if request.OutputMode == modelruntime.OutputJSON {
		format = "json_object"
	}
	maxOutputTokens := request.MaxOutputTokens
	return failureTelemetry{responseFormat: format, maxOutputTokens: &maxOutputTokens}
}

func (t failureTelemetry) withDuration(d time.Duration) failureTelemetry {
	t.requestDuration = &d
	return t
}

func (t failureTelemetry) withResponseBytes(n int) failureTelemetry {
	t.responseContentBytes = &n
	return t
}

func (t failureTelemetry) withFinishReason(reason string) failureTelemetry {
	t.finishReason = reason
	return t
}

// withUsage restates the provider's usage object directly on the outcome
// row, same rationale as deepseek's withUsage: a failure row must be
// self-sufficient without a join, since not every failed attempt gets a
// model_invocation_usage row.
func (t failureTelemetry) withUsage(usage chatUsage) failureTelemetry {
	input := usage.PromptTokens
	output := usage.CompletionTokens
	t.usageAvailable = true
	t.inputTokens = &input
	t.outputTokens = &output
	t.cacheHitTokens, t.cacheMissTokens = cacheTokens(usage)
	return t
}

func (t failureTelemetry) withJSONDecodeFailure(err error, body []byte) failureTelemetry {
	class, offset := classifyJSONError(err)
	t.jsonErrorClass = class
	t.jsonErrorOffset = offset
	starts, ends := jsonBoundaryFlags(body)
	t.startsWithJSONObject = &starts
	t.endsWithJSONObject = &ends
	return t
}

func classifyJSONError(err error) (class string, offset *int64) {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntaxErr):
		value := syntaxErr.Offset
		return "syntax_error", &value
	case errors.As(err, &typeErr):
		value := typeErr.Offset
		return "unmarshal_type_error", &value
	default:
		return "unknown_error", nil
	}
}

func jsonBoundaryFlags(body []byte) (startsWithObject, endsWithObject bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false, false
	}
	return trimmed[0] == '{', trimmed[len(trimmed)-1] == '}'
}

func New(config Config) (*Adapter, error) {
	return newAdapter(config, nil, time.Now)
}

func newAdapter(config Config, client *http.Client, now func() time.Time) (*Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}
	endpoint, _ := url.Parse(config.EndpointURL)
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	if client == nil {
		client = defaultHTTPClient(config.RequestTimeout)
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("mimo redirects are forbidden")
	}
	if now == nil {
		now = time.Now
	}
	descriptor := modelruntime.AdapterDescriptor{
		ProviderID: ProviderID, AdapterID: AdapterID, AdapterVersion: AdapterVersion,
		Transport: modelruntime.TransportHTTP, RequestSchemaVersion: RequestSchemaVersion,
		ResponseSchemaVersion: ResponseSchemaVersion,
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte(endpoint.String())),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte(config.CredentialFile)),
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	config.EndpointURL = endpoint.String()
	return &Adapter{config: config, client: client, descriptor: descriptor, breaker: newCircuitBreaker(config.FailureThreshold, config.OpenDuration), now: now}, nil
}

func (*Adapter) ProviderID() string                           { return ProviderID }
func (a *Adapter) Descriptor() modelruntime.AdapterDescriptor { return a.descriptor }

func (a *Adapter) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ProviderID != ProviderID || strings.TrimSpace(request.ProviderModelID) == "" || request.Deadline.IsZero() {
		return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("invalid_request", "provider_scope_invalid", failureTelemetry{}), Cause: modelruntime.ErrInvalidRequest}
	}
	if !request.Deadline.After(a.now()) {
		return context.DeadlineExceeded
	}
	if !a.breaker.allow(a.now()) {
		return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("circuit_breaker", "circuit_open", failureTelemetry{}), Cause: modelruntime.ErrProviderUnavailable}
	}
	token, err := secrets.LoadBearerToken(a.config.CredentialFile)
	if err != nil {
		return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("credential", "credential_unavailable", failureTelemetry{}), Cause: err}
	}
	secrets.Zero(token)
	return nil
}

func (a *Adapter) Dispatch(ctx context.Context, request modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	baseTelemetry := requestTelemetry(request)
	body, err := encodeRequest(request)
	if err != nil {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("request_encoding", "request_encoding_failed", baseTelemetry), Cause: err}
	}
	token, err := secrets.LoadBearerToken(a.config.CredentialFile)
	if err != nil {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("credential", "credential_unavailable", baseTelemetry), Cause: err}
	}
	defer secrets.Zero(token)

	requestCtx := ctx
	cancel := func() {}
	if a.config.RequestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, a.config.RequestTimeout)
	}
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.config.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("request_build", "request_build_failed", baseTelemetry), Cause: err}
	}
	// MiMo's documented auth header is "api-key: <key>", NOT
	// "Authorization: Bearer" (audit section B, confirmed live against
	// /v1/chat/completions). Using Bearer here would be silently wrong.
	httpRequest.Header.Set("api-key", string(token))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Client-Request-Id", request.ProviderIdempotencyKey)

	sendStart := a.now()
	response, err := a.client.Do(httpRequest)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			a.breaker.failure(a.now())
		}
		telemetry := baseTelemetry.withDuration(a.now().Sub(sendStart))
		outcome := modelruntime.ProviderOutcome{
			OutcomeClassification: modelruntime.ProviderOutcomeAmbiguous,
			ErrorClass:            "transport", ErrorCode: classifyTransportError(err), Retryable: true,
			ResponseSchemaVersion: ResponseSchemaVersion,
			ResponseFormat:        telemetry.responseFormat, MaxOutputTokens: telemetry.maxOutputTokens,
			RequestDuration: telemetry.requestDuration,
		}
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureAmbiguous, Outcome: outcome, Cause: err}
	}
	defer response.Body.Close()
	responseBody, readErr := readBounded(response.Body, a.config.MaxResponseBytes)
	responseHash := modelruntime.SHA256Bytes(responseBody)
	providerRequestID := strings.TrimSpace(response.Header.Get("x-request-id"))
	telemetry := baseTelemetry.withDuration(a.now().Sub(sendStart)).withResponseBytes(len(responseBody))
	if readErr != nil {
		// A deadline or cancellation while the body is still arriving leaves
		// the call AMBIGUOUS, not rejected: the provider accepted the request
		// and may finish and bill it. The rule is shared rather than restated
		// here -- it was restated in six adapters and two of those copies
		// ended a campaign for a transient failure.
		if modelruntime.IsIncompleteRead(readErr) {
			if !modelruntime.IsCallerCancellation(readErr) {
				a.breaker.failure(a.now())
			}
			outcome := modelruntime.IncompleteReadOutcome(response.StatusCode, providerRequestID, responseHash, ResponseSchemaVersion)
			return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureAmbiguous, Outcome: outcome, Cause: readErr}
		}
		a.breaker.failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_read_failed", response.StatusCode >= 500, telemetry)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := retryableStatus(response.StatusCode)
		if retryable {
			a.breaker.failure(a.now())
		}
		class, code := parseProviderError(responseBody)
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, class, code, retryable, telemetry)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	var decoded chatResponse
	if err = json.Unmarshal(responseBody, &decoded); err != nil {
		a.breaker.failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_json_invalid", false, telemetry.withJSONDecodeFailure(err, responseBody))
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	// The response envelope's own "id" field is the provider request/
	// response identifier to persist (audit section D): no separate
	// x-request-id header was observed in real testing for MiMo, so the
	// header (if a future deployment does send one) is preferred when
	// present, exactly like deepseek's fallback-to-decoded.ID pattern.
	if providerRequestID == "" {
		providerRequestID = strings.TrimSpace(decoded.ID)
	}
	telemetry = telemetry.withUsage(decoded.Usage)
	cacheHit, cacheMiss := cacheTokens(decoded.Usage)
	usageOnlyResponse := modelruntime.RawResponse{
		ProviderRequestID: providerRequestID,
		InputTokens:       decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
		ProviderReported:     true,
		PromptCacheHitTokens: cacheHit, PromptCacheMissTokens: cacheMiss,
	}
	if len(decoded.Choices) != 1 {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_choice_count_invalid", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	finishReason := strings.TrimSpace(decoded.Choices[0].FinishReason)
	telemetry = telemetry.withFinishReason(finishReason)
	content, err := decodeContent(decoded.Choices[0].Message.Content)
	if err != nil {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_invalid", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	toolIntents := make([]modelruntime.RawToolIntent, 0, len(decoded.Choices[0].Message.ToolCalls))
	for _, call := range decoded.Choices[0].Message.ToolCalls {
		toolIntents = append(toolIntents, modelruntime.RawToolIntent{ID: call.ID, Name: call.Function.Name, Arguments: append([]byte(nil), call.Function.Arguments...)})
	}
	// finish_reason:"abort" was observed once, only on mimo-v2.5-pro, cause
	// unknown (audit section C/D). Headers and a full body were received,
	// so this is classified AdapterFailureResponseReceived like every other
	// business rejection here -- never ambiguous -- under its own distinct,
	// named error code so it is never conflated with an ordinary truncation
	// (finish_reason:"length").
	switch {
	case finishReason == "abort":
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_aborted", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	case finishReason == "length" && len(content) == 0 && len(toolIntents) == 0:
		// Same reasoning as deepseek: with thinking enabled, an
		// insufficient max_completion_tokens budget can be entirely
		// consumed by reasoning_content, leaving nothing usable in
		// content (audit section E, confirmed live on mimo-v2.5-pro with
		// max_completion_tokens=20).
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_truncated_empty", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	a.breaker.success()
	outcome := modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     providerRequestID, HTTPStatus: response.StatusCode,
		ResponseHash: responseHash, ResponseSchemaVersion: ResponseSchemaVersion,
		FinishReason: bound(telemetry.finishReason, 120), ResponseContentBytes: telemetry.responseContentBytes,
		UsageAvailable: telemetry.usageAvailable, InputTokens: telemetry.inputTokens, OutputTokens: telemetry.outputTokens,
		CacheHitTokens: telemetry.cacheHitTokens, CacheMissTokens: telemetry.cacheMissTokens,
		ResponseFormat: telemetry.responseFormat, MaxOutputTokens: telemetry.maxOutputTokens,
		RequestDuration: telemetry.requestDuration,
	}
	return modelruntime.RawResponse{
		Content: content, ToolIntents: toolIntents,
		HiddenReasoning:   []byte(decoded.Choices[0].Message.ReasoningContent),
		ProviderRequestID: providerRequestID,
		InputTokens:       decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
		ProviderReported: true, ProviderOutcome: outcome,
		PromptCacheHitTokens: cacheHit, PromptCacheMissTokens: cacheMiss,
	}, nil
}

func encodeRequest(request modelruntime.CanonicalRequest) ([]byte, error) {
	if request.ProviderID != ProviderID || strings.TrimSpace(request.ProviderModelID) == "" || request.MaxOutputTokens <= 0 {
		return nil, modelruntime.ErrInvalidRequest
	}
	messages, tools, err := encodeModelInput(request)
	if err != nil {
		return nil, err
	}
	if request.OutputMode == modelruntime.OutputJSON && len(request.OutputSchema) > 0 {
		instruction, err := jsonObjectModeInstruction(request.OutputSchema)
		if err != nil {
			return nil, err
		}
		messages[0].Content = messages[0].Content + "\n\n" + instruction
	}
	payload := chatRequest{
		Model:               request.ProviderModelID,
		Messages:            messages,
		Temperature:         request.Temperature,
		MaxCompletionTokens: request.MaxOutputTokens,
		Thinking:            alwaysEnabledThinking,
		Tools:               tools,
	}
	if request.OutputMode == modelruntime.OutputJSON {
		// MiMo documents response_format:{"type":"json_object"} as
		// guaranteeing syntactically valid JSON only, not a specific
		// structure -- same caveat as DeepSeek (audit section F). There is
		// no evidence this endpoint accepts a stricter json_schema mode, so
		// none is attempted; schema conformance is enforced afterward by
		// internal/modelruntime.Normalizer, generically, the same way it
		// already is for every provider.
		payload.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
	}
	return json.Marshal(payload)
}

func encodeModelInput(request modelruntime.CanonicalRequest) ([]chatMessage, []chatTool, error) {
	input := request.ModelInput.Envelope
	if input.SchemaVersion == "" {
		if len(request.RenderedContext) == 0 {
			return nil, nil, modelruntime.ErrInvalidRequest
		}
		return []chatMessage{{Role: "user", Content: string(request.RenderedContext)}}, nil, nil
	}
	if input.SchemaVersion != modelruntime.ModelInputEnvelopeSchemaV1 || input.ProviderContinuationRef != "" {
		return nil, nil, modelruntime.ErrInvalidRequest
	}
	messages := make([]chatMessage, 0, len(input.StablePrefix)+len(input.VisibleHistory))
	appendMessage := func(source modelruntime.ModelInputMessage) {
		message := chatMessage{Role: string(source.Role), Content: source.Content, ToolCallID: source.ToolCallID}
		for _, sourceCall := range source.ToolCalls {
			call := chatToolCall{ID: sourceCall.ID, Type: "function"}
			call.Function.Name = sourceCall.Name
			call.Function.Arguments = append([]byte(nil), sourceCall.Arguments...)
			message.ToolCalls = append(message.ToolCalls, call)
		}
		messages = append(messages, message)
	}
	for _, message := range input.StablePrefix {
		appendMessage(message)
	}
	for _, message := range input.VisibleHistory {
		appendMessage(message)
	}
	tools := make([]chatTool, 0, len(input.ToolDefinitions))
	for _, source := range input.ToolDefinitions {
		tools = append(tools, chatTool{Type: "function", Function: chatToolFunction{Name: source.Name, Description: source.Description, Parameters: append([]byte(nil), source.InputSchema...)}})
	}
	return messages, tools, nil
}

// jsonObjectModeInstruction renders the output schema as an explicit
// textual contract appended to the prompt, same mechanism as DeepSeek's
// jsonObjectModeInstruction, PLUS an explicit anti-markdown-fence
// instruction that DeepSeek's does not need.
//
// Real, confirmed behavior difference (audit section F): without this
// explicit instruction, mimo-v2.5 wraps its JSON output in a markdown code
// fence (```json ... ```) even in json_object mode, which the provider's
// own guarantee (syntactically valid JSON) does not forbid -- json_object
// mode only constrains the model to emit valid JSON *somewhere*, not to
// omit surrounding prose/markup. The owner's own audit explicitly chose
// "instruction in the prompt" over "silently strip fences in the parser"
// as the primary defense, following MiMo's own documented recommendation
// ("Enforce JSON-Only Output"). This adapter additionally applies a
// defensive, belt-and-suspenders fence-strip in decodeContent (see its doc
// comment) since the cost of stripping a wrapping fence is near zero and
// it cannot be exercised against the real API in this offline-only task.
func jsonObjectModeInstruction(schema json.RawMessage) (string, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, schema, "", "  "); err != nil {
		return "", err
	}
	return "You MUST return exactly one JSON object as your entire response, conforming exactly to the following JSON Schema contract. " +
		"Do not include any text before or after the JSON object. " +
		"Do not wrap the JSON object in a markdown code fence (no ``` characters anywhere in your response). " +
		"Do not include any explanation, preamble, or commentary -- output only the raw JSON object itself, nothing else.\n\n" +
		"JSON Schema:\n" + pretty.String(), nil
}

// decodeContent mirrors deepseek's decodeContent (string or [{type,text}]
// content shapes), plus a defensive strip of a wrapping ```json / ```
// markdown fence when the entire trimmed content is fenced end-to-end.
//
// This is deliberate belt-and-suspenders parsing leniency, not the primary
// defense against MiMo's confirmed fencing behavior (see
// jsonObjectModeInstruction's doc comment for why the prompt instruction is
// primary). It is scoped narrowly -- only strips a fence that wraps the
// *entire* content, never a fence appearing mid-text -- so it cannot alter
// or corrupt genuine, unfenced JSON content.
func decodeContent(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return stripMarkdownFence([]byte(text)), nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, err
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "output_text" {
			builder.WriteString(part.Text)
		}
	}
	return stripMarkdownFence([]byte(builder.String())), nil
}

func stripMarkdownFence(content []byte) []byte {
	trimmed := bytes.TrimSpace(content)
	if !bytes.HasPrefix(trimmed, []byte("```")) {
		return content
	}
	if !bytes.HasSuffix(trimmed, []byte("```")) {
		return content
	}
	inner := trimmed[3 : len(trimmed)-3]
	// Drop an optional language tag ("json") on the fence's opening line.
	if newline := bytes.IndexByte(inner, '\n'); newline >= 0 {
		firstLine := bytes.TrimSpace(inner[:newline])
		if len(firstLine) > 0 && !bytes.ContainsAny(firstLine, "{}[]\"") {
			inner = inner[newline+1:]
		}
	}
	inner = bytes.TrimSpace(inner)
	if len(inner) == 0 {
		return content
	}
	return inner
}

func readBounded(reader io.Reader, limit int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return body, err
	}
	if len(body) > limit {
		return body[:limit], fmt.Errorf("provider response exceeds %d bytes", limit)
	}
	return body, nil
}

func parseProviderError(body []byte) (string, string) {
	// Real error body shape is NOT VERIFIED for MiMo (audit section H) --
	// this is a best-effort generic envelope parse with a safe fallback,
	// mirroring deepseek's providerErrorEnvelope, deliberately not overfit
	// to any shape without evidence.
	var envelope providerErrorEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return "provider", "http_error"
	}
	class := normalizeProviderToken(envelope.Error.Type, "provider", 120)
	code := "http_error"
	switch value := envelope.Error.Code.(type) {
	case string:
		code = normalizeProviderToken(value, "http_error", 160)
	case float64:
		code = normalizeProviderToken(strconv.FormatFloat(value, 'f', -1, 64), "http_error", 160)
	}
	return class, code
}

func normalizeProviderToken(value, fallback string, maximum int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > maximum {
		return fallback
	}
	previousSeparator := false
	for index, char := range value {
		isAlphaNumeric := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		isSeparator := char == '.' || char == '_' || char == '-'
		if !isAlphaNumeric && !isSeparator {
			return fallback
		}
		if isSeparator && (index == 0 || previousSeparator) {
			return fallback
		}
		previousSeparator = isSeparator
	}
	if previousSeparator {
		return fallback
	}
	return value
}

func responseErrorOutcome(status int, requestID, responseHash, class, code string, retryable bool, telemetry failureTelemetry) modelruntime.ProviderOutcome {
	return modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeRejected,
		ProviderRequestID:     requestID, HTTPStatus: status, ErrorClass: bound(class, 120),
		ErrorCode: bound(code, 160), Retryable: retryable, ResponseHash: responseHash,
		ResponseSchemaVersion: ResponseSchemaVersion,
		FinishReason:          bound(telemetry.finishReason, 120), ResponseContentBytes: telemetry.responseContentBytes,
		UsageAvailable: telemetry.usageAvailable, InputTokens: telemetry.inputTokens, OutputTokens: telemetry.outputTokens,
		CacheHitTokens: telemetry.cacheHitTokens, CacheMissTokens: telemetry.cacheMissTokens,
		ResponseFormat: telemetry.responseFormat, MaxOutputTokens: telemetry.maxOutputTokens,
		RequestDuration: telemetry.requestDuration,
		JSONErrorClass:  bound(telemetry.jsonErrorClass, 120), JSONErrorOffset: telemetry.jsonErrorOffset,
		StartsWithJSONObject: telemetry.startsWithJSONObject, EndsWithJSONObject: telemetry.endsWithJSONObject,
	}
}

func (a *Adapter) notSentOutcome(class, code string, telemetry failureTelemetry) modelruntime.ProviderOutcome {
	return modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeNotSent,
		ErrorClass:            class, ErrorCode: code, ResponseSchemaVersion: ResponseSchemaVersion,
		ResponseFormat: telemetry.responseFormat, MaxOutputTokens: telemetry.maxOutputTokens,
	}
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func classifyTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "transport_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "transport_cancelled"
	}
	return "transport_error"
}

func bound(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
