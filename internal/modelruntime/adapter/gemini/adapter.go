package gemini

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
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/circuitbreaker"
	"github.com/Mireuz13/explorarte-organization/internal/secrets"
)

type Adapter struct {
	config     Config
	client     *http.Client
	descriptor modelruntime.AdapterDescriptor
	breaker    *circuitbreaker.Breaker
	now        func() time.Time
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Stream              bool            `json:"stream"`
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
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

type providerErrorEnvelope struct {
	Error struct {
		Type string `json:"type"`
		Code any    `json:"code"`
	} `json:"error"`
}

// failureTelemetry carries Gate F (Provider Failure Telemetry) facts known
// at a given point during Dispatch, merged onto the modelruntime.ProviderOutcome
// under construction by responseErrorOutcome/notSentOutcome. Every field is
// optional and additive -- the zero value means "not yet known" -- and none
// of it is ever prompt/completion content: byte counts, token counts,
// provider-supplied enum-like tokens, and request-shaping facts (response
// format, max output tokens) that are already public API surface. Mirrors
// internal/modelruntime/adapter/deepseek's failureTelemetry; Gemini's usage
// object carries no prompt-cache split, so there is no cache-token pair here.
type failureTelemetry struct {
	responseFormat       string
	maxOutputTokens      *int
	requestDuration      *time.Duration
	responseContentBytes *int
	finishReason         string
	usageAvailable       bool
	inputTokens          *int64
	outputTokens         *int64
	jsonErrorClass       string
	jsonErrorOffset      *int64
	startsWithJSONObject *bool
	endsWithJSONObject   *bool
}

// requestTelemetry captures the request-shaping facts available before any
// network call: response_format and max_output_tokens are both public API
// surface, never response content.
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
// row so a failure row is self-sufficient without a join -- necessary for
// every rejected outcome, since business-failure paths that never reach
// CompleteInvocation still get a model_invocation_usage row via
// insertRecoveredUsage, but earlier failures (pre-decode) never do.
func (t failureTelemetry) withUsage(usage chatUsage) failureTelemetry {
	input := usage.PromptTokens
	output := usage.CompletionTokens
	t.usageAvailable = true
	t.inputTokens = &input
	t.outputTokens = &output
	return t
}

// withJSONDecodeFailure captures the Go encoding/json error's offset and
// type name (never the JSON body itself), plus two cheap boundary checks on
// the raw bytes that distinguish "provider sent something that isn't JSON
// at all" from "provider sent JSON that was truncated mid-object". This is
// what turned the r17-v13 department.worker failure -- gemini-3.5-flash-lite
// returning HTTP 200 with a business result that failed host-side schema
// normalization -- from an unexplained response_normalization_failed into a
// diagnosable one: without it, the outcome row carried zero of these facts.
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
		return errors.New("gemini redirects are forbidden")
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
	return &Adapter{config: config, client: client, descriptor: descriptor, breaker: circuitbreaker.New(config.FailureThreshold, config.OpenDuration), now: now}, nil
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
	if !a.breaker.Allow(a.now()) {
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
	httpRequest.Header.Set("Authorization", "Bearer "+string(token))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Client-Request-Id", request.ProviderIdempotencyKey)

	sendStart := a.now()
	response, err := a.client.Do(httpRequest)
	if err != nil {
		// Caller cancellation is not provider instability and must not open the
		// circuit. Timeouts and transport failures remain ambiguous because the
		// client cannot prove whether the upstream accepted the request.
		if !errors.Is(err, context.Canceled) {
			a.breaker.Failure(a.now())
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
				a.breaker.Failure(a.now())
			}
			outcome := modelruntime.IncompleteReadOutcome(response.StatusCode, providerRequestID, responseHash, ResponseSchemaVersion)
			return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureAmbiguous, Outcome: outcome, Cause: readErr}
		}
		a.breaker.Failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_read_failed", response.StatusCode >= 500, telemetry)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := retryableStatus(response.StatusCode)
		if retryable {
			a.breaker.Failure(a.now())
		}
		class, code := parseProviderError(responseBody)
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, class, code, retryable, telemetry)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	var decoded chatResponse
	if err = json.Unmarshal(responseBody, &decoded); err != nil {
		a.breaker.Failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_json_invalid", false, telemetry.withJSONDecodeFailure(err, responseBody))
		// No usage object was ever decoded here -- json.Unmarshal itself
		// failed, so there is nothing recoverable to attach.
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	if providerRequestID == "" {
		providerRequestID = strings.TrimSpace(decoded.ID)
	}
	telemetry = telemetry.withUsage(decoded.Usage)
	// From this point on, decoded.Usage is known even if the response is
	// about to be rejected for a business reason (wrong choice count,
	// unparseable content, a missing tool name, a content filter, or an
	// empty truncation) -- json.Unmarshal above already succeeded. Carry it
	// forward on RawResponse instead of discarding it, mirroring the deepseek
	// adapter: this is what lets internal/modelruntime/dispatch_service.go
	// commit a real, provider-reported cost for these calls instead of
	// treating them as free.
	usageOnlyResponse := modelruntime.RawResponse{
		ProviderRequestID: providerRequestID,
		InputTokens:       decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
		ProviderReported: true,
	}
	if len(decoded.Choices) != 1 {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_choice_count_invalid", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	telemetry = telemetry.withFinishReason(strings.TrimSpace(decoded.Choices[0].FinishReason))
	content, err := decodeContent(decoded.Choices[0].Message.Content)
	if err != nil {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_invalid", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	tools := make([]modelruntime.RawToolIntent, 0, len(decoded.Choices[0].Message.ToolCalls))
	for _, call := range decoded.Choices[0].Message.ToolCalls {
		if strings.TrimSpace(call.Function.Name) == "" {
			return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "tool_call_name_missing", false, telemetry), Cause: modelruntime.ErrResponseRejected}
		}
		tools = append(tools, modelruntime.RawToolIntent{ID: call.ID, Name: call.Function.Name, Arguments: append([]byte(nil), call.Function.Arguments...)})
	}
	// A provider-side content block, or a truncated response with nothing
	// usable, must never be classified as success: the caller would silently
	// receive an empty result instead of an actionable failure. Observed live
	// against Gemini's OpenAI-compatibility layer: a too-small token budget
	// can be consumed entirely by invisible reasoning, returning
	// finish_reason=length with empty content and zero tool calls.
	switch finish := strings.TrimSpace(decoded.Choices[0].FinishReason); {
	case finish == "content_filter":
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_filtered", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	case finish == "length" && len(content) == 0 && len(tools) == 0:
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_truncated_empty", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	a.breaker.Success()
	outcome := modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     providerRequestID, HTTPStatus: response.StatusCode,
		ResponseHash: responseHash, ResponseSchemaVersion: ResponseSchemaVersion,
		FinishReason: bound(telemetry.finishReason, 120), ResponseContentBytes: telemetry.responseContentBytes,
		UsageAvailable: telemetry.usageAvailable, InputTokens: telemetry.inputTokens, OutputTokens: telemetry.outputTokens,
		ResponseFormat: telemetry.responseFormat, MaxOutputTokens: telemetry.maxOutputTokens,
		RequestDuration: telemetry.requestDuration,
	}
	return modelruntime.RawResponse{
		Content: content, ToolIntents: tools, ProviderRequestID: providerRequestID,
		InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
		ProviderReported: true, ProviderOutcome: outcome,
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
	payload := chatRequest{
		Model:           request.ProviderModelID,
		Messages:        messages,
		Temperature:     request.Temperature,
		ReasoningEffort: request.ReasoningEffort, Stream: false, Tools: tools,
	}
	// Google's OpenAI-compatibility layer for Gemini accepts both max_tokens
	// and max_completion_tokens plus reasoning_effort, and applies them to
	// its native thinking budget (verified live: reasoning_effort=low
	// measurably reduced hidden thinking-token spend vs. an unset request).
	// Gemini spends part of max_tokens/max_completion_tokens on invisible
	// thinking before any visible content — a small budget can be consumed
	// entirely by thinking, producing finish_reason=length with empty
	// content (also observed live). Mirror the same field split used for
	// openaicompat/deepseek for consistency across providers.
	if strings.TrimSpace(request.ReasoningEffort) != "" {
		payload.MaxCompletionTokens = request.MaxOutputTokens
	} else {
		payload.MaxTokens = request.MaxOutputTokens
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
