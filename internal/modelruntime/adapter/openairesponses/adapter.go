package openairesponses

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

// Request/response shapes below follow OpenAI's Responses API
// (POST /v1/responses), confirmed against OpenAI's own current API
// reference rather than assumed from the Chat Completions shape:
//   - input is a message array, not "messages".
//   - reasoning effort is a nested {"reasoning":{"effort":...}} object, not
//     a flat "reasoning_effort" string field.
//   - output is an array of typed items (message, reasoning, function_call,
//     ...); assistant text lives at output[].content[].text where
//     content[].type == "output_text", not output[].message.content.
//   - usage uses input_tokens/output_tokens (not prompt_tokens/
//     completion_tokens).
type responsesRequest struct {
	Model           string              `json:"model"`
	Input           []responseInputItem `json:"input"`
	Instructions    string              `json:"instructions,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	Reasoning       *reasoningConfig    `json:"reasoning,omitempty"`
	Text            *textConfig         `json:"text,omitempty"`
	Store           bool                `json:"store"`
	Stream          bool                `json:"stream"`
	Tools           []responseTool      `json:"tools,omitempty"`
}

type responseInputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type reasoningConfig struct {
	Effort string `json:"effort,omitempty"`
}

type textConfig struct {
	Format json.RawMessage `json:"format,omitempty"`
}

type responsesResponse struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	Model             string             `json:"model"`
	Status            string             `json:"status"`
	Error             *responsesAPIError `json:"error"`
	Output            []responseOutput   `json:"output"`
	Usage             responsesUsage     `json:"usage"`
	IncompleteDetails *incompleteDetails `json:"incomplete_details"`
}

type incompleteDetails struct {
	Reason string `json:"reason"`
}

type responsesAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

type responseOutput struct {
	Type      string                  `json:"type"`
	Content   []responseOutputContent `json:"content,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Arguments json.RawMessage         `json:"arguments,omitempty"`
	CallID    string                  `json:"call_id,omitempty"`
}

type responseOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
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
// of it is ever prompt/completion/reasoning content: byte counts, token
// counts, provider-supplied enum-like tokens, and request-shaping facts
// (response format, max output tokens) that are already public API surface.
// Mirrors internal/modelruntime/adapter/deepseek's failureTelemetry, adapted
// to the Responses API shape: finishReason carries status (and, combined,
// incomplete_details.reason) rather than a chat-completions finish_reason,
// and there is no prompt-cache pair here.
//
// TODO(gate-f-responses): OpenAI's Responses API additionally reports
// usage.output_tokens_details.reasoning_tokens -- the exact figure this
// organization's own live incident (Luna's synthesis call consuming its
// entire output budget on invisible reasoning, finish reason "incomplete",
// zero visible text, non-zero real cost) needed to diagnose without a raw
// response dump. Capturing it here would require a new field on the shared
// modelruntime.ProviderOutcome (and a migration), which is out of scope for
// telemetry PARITY work; this is the specific gap left for that follow-up.
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

// statusTelemetryReason is this adapter's analog of a chat-completions
// finish_reason: the Responses API's top-level status, combined with
// incomplete_details.reason when present (e.g. "incomplete:max_output_tokens")
// so the single bounded finishReason string on the outcome row carries the
// same diagnostic value the adapter already computes for its own
// response_incomplete_* error codes, without duplicating that logic.
func statusTelemetryReason(status string, details *incompleteDetails) string {
	if details != nil && strings.TrimSpace(details.Reason) != "" {
		return status + ":" + strings.TrimSpace(details.Reason)
	}
	return status
}

// withUsage restates the provider's usage object directly on the outcome
// row so a failure row is self-sufficient without a join -- necessary for
// every rejected outcome, since business-failure paths that never reach
// CompleteInvocation still get a model_invocation_usage row via
// insertRecoveredUsage, but earlier failures (pre-decode) never do.
func (t failureTelemetry) withUsage(usage responsesUsage) failureTelemetry {
	input := usage.InputTokens
	output := usage.OutputTokens
	t.usageAvailable = true
	t.inputTokens = &input
	t.outputTokens = &output
	return t
}

// withJSONDecodeFailure captures the Go encoding/json error's offset and
// type name (never the JSON body itself), plus two cheap boundary checks on
// the raw bytes that distinguish "provider sent something that isn't JSON
// at all" from "provider sent JSON that was truncated mid-object".
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
		return errors.New("openai-responses redirects are forbidden")
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
	var decoded responsesResponse
	if err = json.Unmarshal(responseBody, &decoded); err != nil {
		a.breaker.Failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_json_invalid", false, telemetry.withJSONDecodeFailure(err, responseBody))
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	if providerRequestID == "" {
		providerRequestID = strings.TrimSpace(decoded.ID)
	}
	telemetry = telemetry.withUsage(decoded.Usage).withFinishReason(statusTelemetryReason(decoded.Status, decoded.IncompleteDetails))
	if decoded.Error != nil {
		// The Responses API top-level error object observed in practice is
		// {"code": "server_error", "message": ...} -- no "type" key at all
		// (confirmed against OpenAI'''s own response.failed event example).
		// Code is a string here, unlike the Chat Completions error envelope
		// where code can be a string or a numeric HTTP-style code, so this
		// intentionally does not reuse parseProviderError'''s numeric branch.
		code := "response_error"
		if raw, ok := decoded.Error.Code.(string); ok {
			code = normalizeProviderToken(raw, "response_error", 160)
		} else if strings.TrimSpace(decoded.Error.Type) != "" {
			code = normalizeProviderToken(decoded.Error.Type, "response_error", 160)
		}
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", code, false, telemetry)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	// The outer Responses envelope has been decoded successfully at this
	// point, so provider-reported usage is recoverable even if the business
	// response is about to be rejected. Preserve that usage on every
	// post-decode failure just as the DeepSeek adapter does: DispatchService
	// can then commit the real cost instead of parking/releasing an estimate.
	usageOnlyResponse := modelruntime.RawResponse{
		ProviderRequestID: providerRequestID,
		InputTokens:       decoded.Usage.InputTokens,
		OutputTokens:      decoded.Usage.OutputTokens,
		ProviderReported:  true,
	}

	content, tools, err := decodeOutput(decoded.Output)
	if err != nil {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_invalid", false, telemetry)
		return usageOnlyResponse, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}

	// A structured-output contract cannot safely consume a partial Responses
	// result. Even when the visible prefix happens to be syntactically valid
	// JSON, status=="incomplete" means the provider did not complete the
	// requested answer contract. Reject it before Normalizer so it receives
	// the precise provider failure classification rather than the generic
	// response_normalization_failed business error.
	//
	// Text callers retain the existing behavior: partial visible text is
	// usable when present. Empty incomplete responses remain failures for all
	// output modes.
	if decoded.Status == "incomplete" {
		rejectIncomplete := request.OutputMode == modelruntime.OutputJSON ||
			(len(content) == 0 && len(tools) == 0)

		if rejectIncomplete {
			reason := "response_incomplete"
			if len(content) == 0 && len(tools) == 0 {
				reason = "response_incomplete_empty"
			}
			if decoded.IncompleteDetails != nil && strings.TrimSpace(decoded.IncompleteDetails.Reason) != "" {
				reason = normalizeProviderToken(
					"response_incomplete_"+decoded.IncompleteDetails.Reason,
					reason,
					160,
				)
			}

			outcome := responseErrorOutcome(
				response.StatusCode,
				providerRequestID,
				responseHash,
				"response",
				reason,
				false,
				telemetry,
			)
			return usageOnlyResponse, &modelruntime.AdapterError{
				Phase:   modelruntime.AdapterFailureResponseReceived,
				Outcome: outcome,
				Cause:   modelruntime.ErrResponseRejected,
			}
		}
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
		InputTokens: decoded.Usage.InputTokens, OutputTokens: decoded.Usage.OutputTokens,
		ProviderReported: true, ProviderOutcome: outcome,
	}, nil
}

func encodeRequest(request modelruntime.CanonicalRequest) ([]byte, error) {
	if request.ProviderID != ProviderID || strings.TrimSpace(request.ProviderModelID) == "" || request.MaxOutputTokens <= 0 {
		return nil, modelruntime.ErrInvalidRequest
	}
	payload := responsesRequest{
		Model:           request.ProviderModelID,
		Input:           nil,
		MaxOutputTokens: request.MaxOutputTokens,
		Temperature:     request.Temperature,
		Store:           false,
		Stream:          false,
	}
	input, tools, err := encodeModelInput(request)
	if err != nil {
		return nil, err
	}
	payload.Input = input
	payload.Tools = tools
	if strings.TrimSpace(request.ReasoningEffort) != "" {
		payload.Reasoning = &reasoningConfig{Effort: request.ReasoningEffort}
	}
	if request.OutputMode == modelruntime.OutputJSON {
		if len(request.OutputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(request.OutputSchema, &schema); err != nil {
				return nil, err
			}
			format, err := json.Marshal(map[string]any{"type": "json_schema", "name": "explorarte_result", "strict": true, "schema": schema})
			if err != nil {
				return nil, err
			}
			payload.Text = &textConfig{Format: format}
		} else {
			payload.Text = &textConfig{Format: json.RawMessage(`{"type":"json_object"}`)}
		}
	}
	return json.Marshal(payload)
}

func encodeModelInput(request modelruntime.CanonicalRequest) ([]responseInputItem, []responseTool, error) {
	input := request.ModelInput.Envelope
	if input.SchemaVersion == "" {
		if len(request.RenderedContext) == 0 {
			return nil, nil, modelruntime.ErrInvalidRequest
		}
		return []responseInputItem{{Type: "message", Role: "user", Content: string(request.RenderedContext)}}, nil, nil
	}
	if input.SchemaVersion != modelruntime.ModelInputEnvelopeSchemaV1 || input.ProviderContinuationRef != "" {
		return nil, nil, modelruntime.ErrInvalidRequest
	}
	items := make([]responseInputItem, 0, len(input.StablePrefix)+len(input.VisibleHistory)*2)
	appendMessage := func(message modelruntime.ModelInputMessage) {
		switch message.Role {
		case modelruntime.ModelInputRoleTool:
			items = append(items, responseInputItem{Type: "function_call_output", CallID: message.ToolCallID, Output: message.Content})
		default:
			if message.Content != "" {
				items = append(items, responseInputItem{Type: "message", Role: string(message.Role), Content: message.Content})
			}
			for _, call := range message.ToolCalls {
				items = append(items, responseInputItem{Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: append([]byte(nil), call.Arguments...)})
			}
		}
	}
	for _, message := range input.StablePrefix {
		appendMessage(message)
	}
	for _, message := range input.VisibleHistory {
		appendMessage(message)
	}
	tools := make([]responseTool, 0, len(input.ToolDefinitions))
	for _, source := range input.ToolDefinitions {
		tools = append(tools, responseTool{Type: "function", Name: source.Name, Description: source.Description, Parameters: append([]byte(nil), source.InputSchema...), Strict: true})
	}
	return items, tools, nil
}

// decodeOutput walks the Responses API output array. Only "message" items
// contribute visible text (from content[].type=="output_text"); "reasoning"
// items are intentionally skipped -- their content is either absent or,
// with include=reasoning.encrypted_content, opaque ciphertext never meant
// to be treated as the answer. "function_call" items become tool intents.
func decodeOutput(items []responseOutput) ([]byte, []modelruntime.RawToolIntent, error) {
	var text strings.Builder
	tools := make([]modelruntime.RawToolIntent, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			if strings.TrimSpace(item.Name) == "" {
				return nil, nil, fmt.Errorf("function_call output item missing name")
			}
			tools = append(tools, modelruntime.RawToolIntent{ID: item.CallID, Name: item.Name, Arguments: append([]byte(nil), item.Arguments...)})
		case "reasoning":
			// Deliberately not surfaced as content: see doc comment above.
		}
	}
	return []byte(text.String()), tools, nil
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
