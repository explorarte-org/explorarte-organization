// Package executionharness is the V2 provider-independent execution loop for
// already-authorized organizational work. It owns cognitive trajectory, not
// task state, model dispatch, authority, memory selection, or pricing.
package executionharness

import "encoding/json"

type RunIdentity struct {
	RunID                string `json:"run_id"`
	OrganizationID       string `json:"organization_id"`
	TaskID               int64  `json:"task_id"`
	AttemptID            int64  `json:"attempt_id"`
	RoleID               string `json:"role_id"`
	ExecutionPrincipalID string `json:"execution_principal_id"`
	CorrelationID        string `json:"correlation_id"`
	CausationID          string `json:"causation_id"`
}

type InitialContext struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Content string `json:"content"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type RunPolicy struct {
	MaxTurns           int    `json:"max_turns"`
	MaxToolCalls       int    `json:"max_tool_calls"`
	ExecutionProfileID string `json:"execution_profile_id"`
	ModelPolicyRef     string `json:"model_policy_ref"`
	BuildRef           string `json:"build_ref"`
}

type RunSpec struct {
	Identity   RunIdentity      `json:"identity"`
	LeaseToken string           `json:"-"`
	Context    InitialContext   `json:"context"`
	Tools      []ToolDefinition `json:"tools"`
	Policy     RunPolicy        `json:"policy"`
}

type FinishReason string

const (
	FinishFinal FinishReason = "final"
	FinishTools FinishReason = "tool_requests"
)

type ToolRequest struct {
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Arguments  json.RawMessage `json:"arguments"`
}

type Usage struct {
	InputTokens       *int64 `json:"input_tokens,omitempty"`
	OutputTokens      *int64 `json:"output_tokens,omitempty"`
	CachedInputTokens *int64 `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   *int64 `json:"reasoning_tokens,omitempty"`
}

// ReasoningTelemetry records only telemetry a provider elects to expose. It
// is observability evidence and is never consulted for authority or task state.
type ReasoningTelemetry struct {
	ExposureKind                  string `json:"exposure_kind,omitempty"`
	Summary                       string `json:"summary,omitempty"`
	ProviderExposedReasoningTrace string `json:"provider_exposed_reasoning_trace,omitempty"`
	OpaqueContinuationRef         string `json:"opaque_continuation_ref,omitempty"`
	Provenance                    string `json:"provenance,omitempty"`
}

type ModelResult struct {
	FinalOutput        string             `json:"final_output,omitempty"`
	ToolRequests       []ToolRequest      `json:"tool_requests,omitempty"`
	FinishReason       FinishReason       `json:"finish_reason"`
	Usage              Usage              `json:"usage,omitempty"`
	InvocationRef      string             `json:"invocation_ref"`
	ReasoningTelemetry ReasoningTelemetry `json:"reasoning_telemetry,omitempty"`
}

type ToolExecutionResult struct {
	Content    json.RawMessage `json:"content,omitempty"`
	Provenance string          `json:"provenance,omitempty"`
}

type Message struct {
	Role         string          `json:"role"`
	Content      string          `json:"content,omitempty"`
	ToolRequests []ToolRequest   `json:"tool_requests,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolResult   json.RawMessage `json:"tool_result,omitempty"`
	ToolError    string          `json:"tool_error,omitempty"`
}

type NormalizedModelRequest struct {
	RunIdentity             RunIdentity     `json:"run_identity"`
	StablePrefix            json.RawMessage `json:"stable_prefix"`
	VisibleHistory          []Message       `json:"visible_history"`
	ProviderContinuationRef string          `json:"provider_continuation_ref,omitempty"`
	CanonicalBytes          []byte          `json:"-"`
	CanonicalDigest         string          `json:"canonical_digest"`
}

type EventType string

const (
	EventRunStarted            EventType = "run_started"
	EventModelRequestPrepared  EventType = "model_request_prepared"
	EventModelResponseRecorded EventType = "model_response_recorded"
	EventToolCallRequested     EventType = "tool_call_requested"
	EventToolCallDenied        EventType = "tool_call_denied"
	EventToolResultRecorded    EventType = "tool_result_recorded"
	EventRunCompleted          EventType = "run_completed"
	EventRunFailed             EventType = "run_failed"
	EventRunLimitReached       EventType = "run_limit_reached"
	EventRunCancelled          EventType = "run_cancelled"
)

// Event is deliberately a value-only append record. Sequence is assigned by
// the store; no API exists to update, delete, or replace it.
type Event struct {
	RunID          string          `json:"run_id"`
	Sequence       uint64          `json:"sequence"`
	Type           EventType       `json:"type"`
	CorrelationID  string          `json:"correlation_id"`
	CausationID    string          `json:"causation_id"`
	IdentityDigest string          `json:"identity_digest,omitempty"`
	RequestDigest  string          `json:"request_digest,omitempty"`
	ModelResult    *ModelResult    `json:"model_result,omitempty"`
	ToolRequest    *ToolRequest    `json:"tool_request,omitempty"`
	ToolResult     json.RawMessage `json:"tool_result,omitempty"`
	ToolProvenance string          `json:"tool_provenance,omitempty"`
	TerminalStatus RunStatus       `json:"terminal_status,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
	Reason         string          `json:"reason,omitempty"`
}

type RunStatus string

const (
	StatusCompleted           RunStatus = "completed"
	StatusLimitReached        RunStatus = "limit_reached"
	StatusModelError          RunStatus = "model_error"
	StatusToolError           RunStatus = "tool_error"
	StatusAuthorizationDenied RunStatus = "authorization_denied"
	StatusIdentityDrift       RunStatus = "identity_drift"
	StatusHistoryError        RunStatus = "history_error"
	StatusCancelled           RunStatus = "cancelled"
)

type RunResult struct {
	RunID             string    `json:"run_id"`
	Status            RunStatus `json:"status"`
	FinalOutput       string    `json:"final_output,omitempty"`
	LastModelOutput   string    `json:"last_model_output,omitempty"`
	LastSequence      uint64    `json:"last_sequence"`
	TurnsUsed         int       `json:"turns_used"`
	ToolCallsUsed     int       `json:"tool_calls_used"`
	TerminationReason string    `json:"termination_reason"`
	Provenance        string    `json:"provenance"`
}
