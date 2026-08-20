// Package xai adapts xAI's chat-completions API for Model Runtime.
//
// It exists as its own package rather than a second instance of openaicompat
// because ProviderID is a package constant there and the adapter registry
// indexes by it: two instances would collide. That is the same reason
// deepseek, gemini and mimo are separate packages despite three of them
// speaking the OpenAI request shape.
//
// The differences from those neighbours are deliberate and each one is
// grounded in xAI's published contract rather than copied:
//
//   - max_tokens is deprecated by xAI in favour of max_completion_tokens, so
//     this adapter always sends the latter and never the former. gemini and
//     openaicompat switch between the two.
//   - reasoning_effort accepts only none/low/medium/high, and only on the
//     models that support it. An out-of-range value is rejected before the
//     request is built rather than sent and 400'd.
//   - usage carries prompt_tokens_details.cached_tokens, which is mapped onto
//     the prompt-cache fields Model Runtime already understands.
//   - message.reasoning_content is deliberately NOT decoded. The normalizer
//     discards HiddenReasoning anyway; never parsing it is a stronger
//     guarantee than parsing and dropping it, and the adversarial reviewer's
//     private reasoning is exactly the thing that must not become durable.
package xai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// supportedReasoningEfforts is xAI's documented set. It is a closed set here
// so that a routing policy carrying, say, "xhigh" -- a value other providers
// in this repository do use -- fails before the provider call instead of
// spending one to be told no.
var supportedReasoningEfforts = map[string]struct{}{
	"none": {}, "low": {}, "medium": {}, "high": {},
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Stream              bool            `json:"stream"`
	StreamOptions       *streamOptions  `json:"stream_options,omitempty"`
	Tools               []chatTool      `json:"tools,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
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

type chatResponse struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

// chatResponseMessage intentionally omits reasoning_content. See the package
// comment: not decoding it is the guarantee.
type chatResponseMessage struct {
	Content   json.RawMessage `json:"content"`
	ToolCalls []chatToolCall  `json:"tool_calls"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type chatUsage struct {
	PromptTokens       int64                `json:"prompt_tokens"`
	CompletionTokens   int64                `json:"completion_tokens"`
	PromptTokensDetail *promptTokensDetails `json:"prompt_tokens_details"`
	// CompletionTokensDetail carries the reasoning count, which xAI reports
	// SEPARATELY from completion_tokens rather than inside it.
	CompletionTokensDetail *completionTokensDetails `json:"completion_tokens_details"`
}

type completionTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// billedOutputTokens is everything the model generated, visible or not.
//
// xAI reports completion_tokens as VISIBLE output only and reasoning
// separately, and its own total confirms the split: 208 prompt + 10
// completion + 1036 reasoning = 1254 total. Reading completion_tokens alone
// therefore recorded 10 output tokens for a call that generated 1046.
//
// Reasoning is billed at the output rate, which xAI's own figure confirms:
// that call reported cost_in_usd_ticks 65000000, and at the configured
// $2/M input and $6/M output, counting reasoning as output gives $0.0067
// while ignoring it gives $0.0005 -- fourteen times under.
//
// This is not an accounting detail. The agent budget's token ceiling and the
// provider wallet both settle against these numbers, so undercounting lets a
// campaign spend past a limit the owner set and the ledger believe money is
// available that is not. It undercounts most exactly where it matters most,
// on a reasoning model at high effort, where thinking is the bulk of the work.
func (u chatUsage) billedOutputTokens() int64 {
	if u.CompletionTokensDetail == nil {
		return u.CompletionTokens
	}
	return u.CompletionTokens + u.CompletionTokensDetail.ReasoningTokens
}

type promptTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

type providerErrorEnvelope struct {
	Error struct {
		Type string `json:"type"`
		Code any    `json:"code"`
	} `json:"error"`
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
		client = defaultHTTPClient()
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("xai redirects are forbidden")
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
		return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("invalid_request", "provider_scope_invalid"), Cause: modelruntime.ErrInvalidRequest}
	}
	if !request.Deadline.After(a.now()) {
		return context.DeadlineExceeded
	}
	if !a.breaker.allow(a.now()) {
		return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("circuit_breaker", "circuit_open"), Cause: modelruntime.ErrProviderUnavailable}
	}
	token, err := secrets.LoadBearerToken(a.config.CredentialFile)
	if err != nil {
		// The error is surfaced as an outcome code only. The credential path
		// is already reduced to a hash in the descriptor and the token itself
		// never leaves this function.
		return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("credential", "credential_unavailable"), Cause: err}
	}
	secrets.Zero(token)
	return nil
}

func (a *Adapter) Dispatch(ctx context.Context, request modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	body, err := encodeRequest(request)
	if err != nil {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("request_encoding", "request_encoding_failed"), Cause: err}
	}
	token, err := secrets.LoadBearerToken(a.config.CredentialFile)
	if err != nil {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("credential", "credential_unavailable"), Cause: err}
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
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: a.notSentOutcome("request_build", "request_build_failed"), Cause: err}
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(token))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("X-Client-Request-Id", request.ProviderIdempotencyKey)

	response, err := a.client.Do(httpRequest)
	if err != nil {
		// Caller cancellation is not provider instability and must not open
		// the circuit. Everything else stays ambiguous: the client cannot
		// prove whether the upstream accepted the request, and guessing here
		// is what produces duplicate provider calls later.
		if !errors.Is(err, context.Canceled) {
			a.breaker.failure(a.now())
		}
		outcome := modelruntime.ProviderOutcome{
			OutcomeClassification: modelruntime.ProviderOutcomeAmbiguous,
			ErrorClass:            "transport", ErrorCode: classifyTransportError(err), Retryable: true,
			ResponseSchemaVersion: ResponseSchemaVersion,
		}
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureAmbiguous, Outcome: outcome, Cause: err}
	}
	defer response.Body.Close()
	// An error status never arrives as a stream, so it is read as the plain
	// JSON body it is. Reassembling it would turn a perfectly readable
	// provider error into "carried no completion events" and lose the reason.
	responseBody, readErr := a.readResponse(response)
	responseHash := modelruntime.SHA256Bytes(responseBody)
	providerRequestID := strings.TrimSpace(response.Header.Get("x-request-id"))
	if readErr != nil {
		// A timeout or cancellation while the body is still arriving is
		// AMBIGUOUS, not a rejection. Headers came back, so the provider
		// accepted the request and may finish and bill it; the client simply
		// stopped listening. Before streaming this could not happen -- a
		// timeout struck at client.Do, which the transport branch above
		// already classifies ambiguous -- so treating a mid-stream timeout
		// as a clean provider rejection would be a regression introduced by
		// streaming, and would let a paid call be repeated as if nothing had
		// been sent.
		//
		// This is a live case, not a precaution: xAI holds a queued request
		// open with keepalive comments and no data events while it is at
		// capacity, so the wait for the first real event can outlast any
		// timeout that is set.
		if errors.Is(readErr, context.DeadlineExceeded) || errors.Is(readErr, context.Canceled) {
			if !errors.Is(readErr, context.Canceled) {
				a.breaker.failure(a.now())
			}
			outcome := modelruntime.ProviderOutcome{
				OutcomeClassification: modelruntime.ProviderOutcomeAmbiguous,
				ProviderRequestID:     providerRequestID,
				HTTPStatus:            response.StatusCode,
				ErrorClass:            "transport", ErrorCode: "stream_incomplete", Retryable: true,
				ResponseHash: responseHash, ResponseSchemaVersion: ResponseSchemaVersion,
			}
			return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureAmbiguous, Outcome: outcome, Cause: readErr}
		}
		a.breaker.failure(a.now())
		// The specific structural reason survives into the durable record.
		// Reporting only "response_read_failed" is what turned the first
		// streaming failure into archaeology that could not be completed.
		// A provider error carried inside the stream arrives with HTTP 200,
		// so retryability cannot come from the status code. It comes from
		// what the provider said.
		retryable := response.StatusCode >= 500 || StreamErrorRetryable(readErr)
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", StreamErrorCode(readErr, "response_read_failed"), retryable)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := retryableStatus(response.StatusCode)
		if retryable {
			a.breaker.failure(a.now())
		}
		class, code := parseProviderError(responseBody)
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, class, code, retryable)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	var decoded chatResponse
	if err = json.Unmarshal(responseBody, &decoded); err != nil {
		a.breaker.failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_json_invalid", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	// xAI documents a response-body id even when no request-id header is
	// present, so provenance survives either way.
	if providerRequestID == "" {
		providerRequestID = strings.TrimSpace(decoded.ID)
	}
	if len(decoded.Choices) != 1 {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_choice_count_invalid", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	content, err := decodeContent(decoded.Choices[0].Message.Content)
	if err != nil {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_invalid", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	tools := make([]modelruntime.RawToolIntent, 0, len(decoded.Choices[0].Message.ToolCalls))
	for _, call := range decoded.Choices[0].Message.ToolCalls {
		if strings.TrimSpace(call.Function.Name) == "" {
			return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "tool_call_name_missing", false), Cause: modelruntime.ErrResponseRejected}
		}
		tools = append(tools, modelruntime.RawToolIntent{ID: call.ID, Name: call.Function.Name, Arguments: append([]byte(nil), call.Function.Arguments...)})
	}
	// A truncated response with nothing usable must not be reported as
	// success: the caller would receive an empty adversarial review instead
	// of an actionable failure. This is a live risk on reasoning models
	// specifically, because reasoning tokens are billed against the
	// completion budget -- xAI reports them under
	// completion_tokens_details.reasoning_tokens -- so a small budget can be
	// consumed entirely before any visible content is produced.
	switch finish := strings.TrimSpace(decoded.Choices[0].FinishReason); {
	case finish == "content_filter":
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_filtered", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	case finish == "length" && len(content) == 0 && len(tools) == 0:
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_truncated_empty", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	a.breaker.success()
	outcome := modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     providerRequestID, HTTPStatus: response.StatusCode,
		ResponseHash: responseHash, ResponseSchemaVersion: ResponseSchemaVersion,
	}
	raw := modelruntime.RawResponse{
		Content: content, ToolIntents: tools, ProviderRequestID: providerRequestID,
		InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.billedOutputTokens(),
		ProviderReported: true, ProviderOutcome: outcome,
	}
	if hit, miss, reported := promptCacheSplit(decoded.Usage); reported {
		raw.PromptCacheHitTokens = &hit
		raw.PromptCacheMissTokens = &miss
	}
	return raw, nil
}

// promptCacheSplit maps xAI's prompt_tokens_details.cached_tokens onto the
// hit/miss pair Model Runtime already records. Both are reported or neither
// is: nil means "the provider did not say", which is a different fact from
// zero. An incoherent split (cached exceeding the prompt total) is treated as
// unreported rather than normalized into something plausible.
func promptCacheSplit(usage chatUsage) (int64, int64, bool) {
	if usage.PromptTokensDetail == nil {
		return 0, 0, false
	}
	cached := usage.PromptTokensDetail.CachedTokens
	if cached < 0 || usage.PromptTokens < 0 || cached > usage.PromptTokens {
		return 0, 0, false
	}
	return cached, usage.PromptTokens - cached, true
}

func encodeRequest(request modelruntime.CanonicalRequest) ([]byte, error) {
	if request.ProviderID != ProviderID || strings.TrimSpace(request.ProviderModelID) == "" || request.MaxOutputTokens <= 0 {
		return nil, modelruntime.ErrInvalidRequest
	}
	effort := strings.TrimSpace(request.ReasoningEffort)
	if effort != "" {
		if _, supported := supportedReasoningEfforts[effort]; !supported {
			return nil, fmt.Errorf("%w: xai reasoning_effort %q is not one of none/low/medium/high", modelruntime.ErrInvalidRequest, effort)
		}
	}
	messages, tools, err := encodeModelInput(request)
	if err != nil {
		return nil, err
	}
	payload := chatRequest{
		Model:    request.ProviderModelID,
		Messages: messages,
		// xAI deprecates max_tokens in favour of max_completion_tokens, so
		// this adapter only ever sends the latter -- unlike the gemini and
		// openaicompat adapters, which choose between them.
		MaxCompletionTokens: request.MaxOutputTokens,
		Temperature:         request.Temperature,
		ReasoningEffort:     effort,
		// Streaming is not a performance choice here. A non-streaming
		// completion sends no bytes until generation finishes, which makes
		// the transport's ResponseHeaderTimeout race the model's entire
		// thinking time -- and on a reasoning model it loses. See stream.go.
		Stream: true,
		// Without this xAI sends no usage chunk at all, and the reassembled
		// document reports zero tokens for a call that really consumed them
		// -- which the cost ledger then settles real spend against.
		StreamOptions: &streamOptions{IncludeUsage: true},
		Tools:         tools,
	}
	if request.OutputMode == modelruntime.OutputJSON {
		if len(request.OutputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(request.OutputSchema, &schema); err != nil {
				return nil, err
			}
			format, err := json.Marshal(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "explorarte_result", "strict": true, "schema": schema}})
			if err != nil {
				return nil, err
			}
			payload.ResponseFormat = format
		} else {
			payload.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
		}
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

func decodeContent(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []byte(text), nil
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
	return []byte(builder.String()), nil
}

// readResponse returns the response document, whatever transport carried it.
//
// The reassembled stream and the plain body are the same shape on purpose:
// everything downstream -- the hash, the choice check, content decoding, tool
// intents, finish reason, usage -- reads one representation and cannot drift
// into handling two.
func (a *Adapter) readResponse(response *http.Response) ([]byte, error) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readBounded(response.Body, a.config.MaxResponseBytes)
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), streamContentType) {
		// The provider ignored the stream flag and answered with a whole
		// document. Honouring that is strictly better than failing: the call
		// succeeded and the bytes are usable.
		return readBounded(response.Body, a.config.MaxResponseBytes)
	}
	return reassembleStream(response.Body, a.config.MaxResponseBytes)
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

// normalizeProviderToken keeps provider-supplied strings from becoming a
// channel into durable state: anything outside a bounded lowercase token
// vocabulary is replaced by a fixed fallback rather than stored.
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

func responseErrorOutcome(status int, requestID, responseHash, class, code string, retryable bool) modelruntime.ProviderOutcome {
	return modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeRejected,
		ProviderRequestID:     requestID, HTTPStatus: status, ErrorClass: bound(class, 120),
		ErrorCode: bound(code, 160), Retryable: retryable, ResponseHash: responseHash,
		ResponseSchemaVersion: ResponseSchemaVersion,
	}
}

func (a *Adapter) notSentOutcome(class, code string) modelruntime.ProviderOutcome {
	return modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeNotSent,
		ErrorClass:            class, ErrorCode: code, ResponseSchemaVersion: ResponseSchemaVersion,
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
