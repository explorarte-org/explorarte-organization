package alibabaclaude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

const (
	fixedSystemPrompt = "You are a bounded model provider. Follow the task supplied on standard input. Do not access files, tools, browsers, MCP servers, plugins, skills, memory, or the surrounding host. Return only the requested answer."
	fixedQueryPrompt  = "Process the task and context supplied on standard input and return the requested answer."
)

type Adapter struct {
	config     Config
	descriptor modelruntime.AdapterDescriptor
	sem        chan struct{}
	now        func() time.Time
}

type cliJSONResponse struct {
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Usage            struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func New(config Config) (*Adapter, error) { return newAdapter(config, time.Now) }

func newAdapter(config Config, now func() time.Time) (*Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}
	if now == nil {
		now = time.Now
	}
	fingerprintMaterial := strings.Join([]string{
		"alibaba-claude-cli-v1", config.TokenPlanBaseURL, config.ExecutableSHA256,
		config.ExpectedVersion, config.SettingsSHA256, config.RuntimePath,
	}, "\x00")
	descriptor := modelruntime.AdapterDescriptor{
		ProviderID: ProviderID, AdapterID: AdapterID, AdapterVersion: AdapterVersion,
		Transport: modelruntime.TransportCLI, RequestSchemaVersion: RequestSchemaVersion,
		ResponseSchemaVersion: ResponseSchemaVersion,
		EndpointFingerprint: modelruntime.SHA256Bytes([]byte(fingerprintMaterial)),
		CredentialRefHash:   modelruntime.SHA256Bytes([]byte(config.SettingsFile)),
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &Adapter{config: config, descriptor: descriptor, sem: make(chan struct{}, config.MaxConcurrency), now: now}, nil
}

func (*Adapter) ProviderID() string                            { return ProviderID }
func (a *Adapter) Descriptor() modelruntime.AdapterDescriptor { return a.descriptor }

func (a *Adapter) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ProviderID != ProviderID || !validModelID(strings.TrimSpace(request.ProviderModelID)) || request.Deadline.IsZero() {
		return a.beforeRequest("preflight", "provider_scope_invalid", modelruntime.ErrInvalidRequest)
	}
	if !request.Deadline.After(a.now()) {
		return context.DeadlineExceeded
	}
	if err := validateInstallation(ctx, a.config); err != nil {
		return a.beforeRequest("preflight", "installation_drift", err)
	}
	return nil
}

func (a *Adapter) Dispatch(ctx context.Context, request modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	if request.ProviderID != ProviderID || !validModelID(strings.TrimSpace(request.ProviderModelID)) || request.MaxOutputTokens <= 0 || request.Deadline.IsZero() {
		return modelruntime.RawResponse{}, a.beforeRequest("request", "request_invalid", modelruntime.ErrInvalidRequest)
	}
	if len(request.RenderedContext) == 0 || len(request.RenderedContext) > maxClaudeStdinBytes {
		return modelruntime.RawResponse{}, a.beforeRequest("request", "stdin_size_invalid", modelruntime.ErrInvalidRequest)
	}
	if request.OutputMode != modelruntime.OutputText && request.OutputMode != modelruntime.OutputJSON {
		return modelruntime.RawResponse{}, a.beforeRequest("request", "output_mode_invalid", modelruntime.ErrInvalidRequest)
	}
	if request.OutputMode == modelruntime.OutputJSON && len(request.OutputSchema) > 0 {
		var schema any
		if err := json.Unmarshal(request.OutputSchema, &schema); err != nil {
			return modelruntime.RawResponse{}, a.beforeRequest("request", "output_schema_invalid", err)
		}
	}
	if request.ReasoningEffort != "" && !validEffort(request.ReasoningEffort) {
		return modelruntime.RawResponse{}, a.beforeRequest("request", "reasoning_effort_invalid", modelruntime.ErrInvalidRequest)
	}
	if !request.Deadline.After(a.now()) {
		return modelruntime.RawResponse{}, a.beforeRequest("deadline", "deadline_elapsed", context.DeadlineExceeded)
	}
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return modelruntime.RawResponse{}, a.beforeRequest("concurrency", "concurrency_wait_cancelled", ctx.Err())
	}
	if err := validateInstallation(ctx, a.config); err != nil {
		return modelruntime.RawResponse{}, a.beforeRequest("preflight", "installation_drift", err)
	}

	args := a.arguments(request)
	env := append(childEnvironment(a.config),
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS="+strconv.Itoa(request.MaxOutputTokens),
		"CLAUDE_CODE_MAX_RETRIES=0",
		"MAX_STRUCTURED_OUTPUT_RETRIES=0",
	)
	stdout, started, exitCode, runErr := runCLI(ctx, cliRunRequest{
		Executable: a.config.Executable, Args: args, Env: env, Dir: a.config.WorkDir,
		Stdin: request.RenderedContext, MaxStdoutBytes: a.config.MaxStdoutBytes,
		MaxStderrBytes: a.config.MaxStderrBytes, Timeout: a.effectiveTimeout(request.Deadline), KillGrace: a.config.KillGrace,
	})
	if runErr != nil {
		if !started {
			return modelruntime.RawResponse{}, a.beforeRequest("process", "process_not_started", runErr)
		}
		return modelruntime.RawResponse{}, a.ambiguous(exitCode, classifyCLIError(runErr, exitCode), runErr)
	}
	if !started || exitCode == nil || *exitCode != 0 {
		return modelruntime.RawResponse{}, a.ambiguous(exitCode, classifyCLIError(modelruntime.ErrProviderUnavailable, exitCode), modelruntime.ErrProviderUnavailable)
	}
	responseHash := modelruntime.SHA256Bytes(stdout)
	content, inputTokens, outputTokens, err := parseCLIResponse(stdout, request.OutputMode)
	if err != nil {
		return modelruntime.RawResponse{}, a.ambiguous(exitCode, "response_contract_invalid", err)
	}
	outcome := modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		Transport: modelruntime.TransportCLI, ProcessExitCode: exitCode,
		ResponseHash: responseHash, ResponseSchemaVersion: ResponseSchemaVersion,
	}
	if err := outcome.Validate(); err != nil {
		return modelruntime.RawResponse{}, a.ambiguous(exitCode, "response_evidence_invalid", err)
	}
	return modelruntime.RawResponse{
		Content: content, InputTokens: inputTokens, OutputTokens: outputTokens,
		ProviderReported: inputTokens > 0 || outputTokens > 0, ProviderOutcome: outcome,
	}, nil
}

func (a *Adapter) arguments(request modelruntime.CanonicalRequest) []string {
	args := []string{
		"--safe-mode",
		"--setting-sources", "",
		"-p", fixedQueryPrompt,
		"--output-format", "json",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
		"--strict-mcp-config",
		"--tools", "",
		"--disallowedTools", "mcp__*",
		"--permission-mode", "dontAsk",
		"--max-turns", "1",
		"--model", request.ProviderModelID,
		"--settings", a.config.SettingsFile,
		"--system-prompt", fixedSystemPrompt,
	}
	if request.ReasoningEffort != "" {
		args = append(args, "--effort", request.ReasoningEffort)
	}
	if request.OutputMode == modelruntime.OutputJSON && len(request.OutputSchema) > 0 {
		args = append(args, "--json-schema", string(request.OutputSchema))
	}
	return args
}

func parseCLIResponse(body []byte, mode modelruntime.OutputMode) ([]byte, int64, int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var response cliJSONResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, 0, 0, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, 0, 0, errors.New("Claude Code returned multiple JSON values")
		}
		return nil, 0, 0, err
	}
	if response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 {
		return nil, 0, 0, errors.New("Claude Code usage is invalid")
	}
	if mode == modelruntime.OutputJSON {
		if len(response.StructuredOutput) == 0 || bytes.Equal(bytes.TrimSpace(response.StructuredOutput), []byte("null")) {
			return nil, 0, 0, errors.New("Claude Code structured_output is missing")
		}
		var value any
		if err := json.Unmarshal(response.StructuredOutput, &value); err != nil {
			return nil, 0, 0, err
		}
		content, err := json.Marshal(value)
		return content, response.Usage.InputTokens, response.Usage.OutputTokens, err
	}
	if strings.TrimSpace(response.Result) == "" {
		return nil, 0, 0, errors.New("Claude Code result is empty")
	}
	return []byte(response.Result), response.Usage.InputTokens, response.Usage.OutputTokens, nil
}

func (a *Adapter) beforeRequest(class, code string, cause error) error {
	return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureBeforeRequest, Outcome: modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeNotSent, Transport: modelruntime.TransportCLI,
		ErrorClass: class, ErrorCode: code, ResponseSchemaVersion: ResponseSchemaVersion,
	}, Cause: cause}
}

func (a *Adapter) ambiguous(exitCode *int, code string, cause error) error {
	outcome := modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeAmbiguous, Transport: modelruntime.TransportCLI,
		ProcessExitCode: exitCode, ErrorClass: "cli_transport", ErrorCode: code,
		Retryable: false, ResponseSchemaVersion: ResponseSchemaVersion,
	}
	return &modelruntime.AdapterError{Phase: modelruntime.AdapterFailureAmbiguous, Outcome: outcome, Cause: cause}
}

func (a *Adapter) effectiveTimeout(deadline time.Time) time.Duration {
	remaining := deadline.Sub(a.now())
	if remaining <= 0 {
		return time.Nanosecond
	}
	if a.config.RequestTimeout < remaining {
		return a.config.RequestTimeout
	}
	return remaining
}

func validEffort(value string) bool {
	switch value {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func classifyCLIError(err error, exitCode *int) string {
	if exitCode != nil && *exitCode >= 0 && *exitCode <= 255 {
		return "process_exit_" + strconv.Itoa(*exitCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "process_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "process_cancelled_ambiguous"
	}
	return "process_failed_ambiguous"
}

var _ modelruntime.ProviderAdapter = (*Adapter)(nil)
