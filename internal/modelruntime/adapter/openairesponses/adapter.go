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
	"github.com/Mireuz13/explorarte-organization/internal/secrets"
)

type Adapter struct {
	config     Config
	client     *http.Client
	descriptor modelruntime.AdapterDescriptor
	breaker    *circuitBreaker
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
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
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
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Client-Request-Id", request.ProviderIdempotencyKey)

	response, err := a.client.Do(httpRequest)
	if err != nil {
		// Caller cancellation is not provider instability and must not open the
		// circuit. Timeouts and transport failures remain ambiguous because the
		// client cannot prove whether the upstream accepted the request.
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
	responseBody, readErr := readBounded(response.Body, a.config.MaxResponseBytes)
	responseHash := modelruntime.SHA256Bytes(responseBody)
	providerRequestID := strings.TrimSpace(response.Header.Get("x-request-id"))
	if readErr != nil {
		a.breaker.failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_read_failed", response.StatusCode >= 500)
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
	var decoded responsesResponse
	if err = json.Unmarshal(responseBody, &decoded); err != nil {
		a.breaker.failure(a.now())
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_json_invalid", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	if providerRequestID == "" {
		providerRequestID = strings.TrimSpace(decoded.ID)
	}
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
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", code, false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	content, tools, err := decodeOutput(decoded.Output)
	if err != nil {
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", "response_content_invalid", false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: err}
	}
	// Same fail-closed rule as the Chat Completions adapter (see its own
	// comment): a response that ran out of budget with nothing usable must
	// never be reported as success just because the HTTP call succeeded.
	// The Responses API reports this as status=="incomplete" with
	// incomplete_details.reason=="max_output_tokens" rather than
	// finish_reason=="length" -- different field, same failure mode
	// (reasoning tokens consuming the entire budget before visible output).
	if decoded.Status == "incomplete" && len(content) == 0 && len(tools) == 0 {
		reason := "response_incomplete_empty"
		if decoded.IncompleteDetails != nil && strings.TrimSpace(decoded.IncompleteDetails.Reason) != "" {
			reason = normalizeProviderToken("response_incomplete_"+decoded.IncompleteDetails.Reason, reason, 160)
		}
		outcome := responseErrorOutcome(response.StatusCode, providerRequestID, responseHash, "response", reason, false)
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureResponseReceived, Outcome: outcome, Cause: modelruntime.ErrResponseRejected}
	}
	a.breaker.success()
	outcome := modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     providerRequestID, HTTPStatus: response.StatusCode,
		ResponseHash: responseHash, ResponseSchemaVersion: ResponseSchemaVersion,
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
