package modelruntime

import (
	"encoding/json"
	"time"
)

// BuildSHA identifies which build of this service is running (P0-F: runtime/
// adapter build identity). It defaults to "unknown" and is intended to be
// overridden at real build time via
// `-ldflags "-X .../internal/modelruntime.BuildSHA=$(git rev-parse HEAD)"`.
// Wiring that ldflags injection into the actual Docker build is a followup —
// see migration 000037's up.sql for the column this feeds and why it isn't
// fully wired yet.
var BuildSHA = "unknown"

type Transport string

const (
	TransportCLI  Transport = "cli_adapter"
	TransportHTTP Transport = "http_adapter"
	TransportFake Transport = "fake_adapter"
)

type AdapterStatus string

const (
	AdapterAvailable   AdapterStatus = "available"
	AdapterUnavailable AdapterStatus = "unavailable"
)

type ModelCapability string

type OutputMode string

const (
	OutputText OutputMode = "text"
	OutputJSON OutputMode = "json"
)

type ThinkingMode string

const (
	ThinkingDisabled ThinkingMode = "disabled"
	ThinkingOpaque   ThinkingMode = "opaque"
)

type InvocationStatus string

const (
	InvocationRequested        InvocationStatus = "requested"
	InvocationClaimed          InvocationStatus = "claimed"
	InvocationSendStarted      InvocationStatus = "send_started"
	InvocationResponseReceived InvocationStatus = "response_received"
	InvocationSucceeded        InvocationStatus = "succeeded"
	InvocationFailed           InvocationStatus = "failed"
	InvocationCancelled        InvocationStatus = "cancelled"
	InvocationAmbiguous        InvocationStatus = "ambiguous"
)

func (s InvocationStatus) Terminal() bool {
	return s == InvocationSucceeded || s == InvocationFailed || s == InvocationCancelled || s == InvocationAmbiguous
}

type DispatchStatus string

const (
	DispatchClaimed          DispatchStatus = "claimed"
	DispatchSendStarted      DispatchStatus = "send_started"
	DispatchResponseReceived DispatchStatus = "response_received"
	DispatchFailedBeforeSend DispatchStatus = "failed_before_send"
	DispatchAmbiguous        DispatchStatus = "ambiguous"
	DispatchCompleted        DispatchStatus = "completed"
)

type RetrySafety string

const (
	RetrySafeBeforeSend  RetrySafety = "safe_before_send"
	RetryUnsafeAfterSend RetrySafety = "unsafe_after_send"
	RetryNotRetryable    RetrySafety = "not_retryable"
)

type Provider struct {
	OrganizationID         string        `json:"organization_id"`
	ID                     string        `json:"id"`
	Transport              Transport     `json:"transport"`
	AdapterStatus          AdapterStatus `json:"adapter_status"`
	DispatchEnabled        bool          `json:"dispatch_enabled"`
	DirectHTTPForbidden    bool          `json:"direct_http_forbidden"`
	CanonicalHash          string        `json:"canonical_hash"`
	OrganizationRevisionID int64         `json:"organization_revision_id"`
	CreatedAt              time.Time     `json:"created_at,omitempty"`
}

type Profile struct {
	OrganizationID string    `json:"organization_id"`
	ID             string    `json:"id"`
	PolicyID       string    `json:"policy_id"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}
type ProfileVersion struct {
	ID                     int64         `json:"id"`
	OrganizationID         string        `json:"organization_id"`
	ProfileID              string        `json:"profile_id"`
	VersionNumber          int           `json:"version_number"`
	OrganizationRevisionID int64         `json:"organization_revision_id"`
	CanonicalDocumentHash  string        `json:"canonical_document_hash"`
	VersionHash            string        `json:"version_hash"`
	ProviderID             string        `json:"provider_id"`
	ProviderModelID        string        `json:"provider_model_id"`
	Transport              Transport     `json:"transport"`
	ReasoningEffort        string        `json:"reasoning_effort,omitempty"`
	DecisionStatus         string        `json:"decision_status,omitempty"`
	AdapterStatus          AdapterStatus `json:"adapter_status"`
	DispatchEnabled        bool          `json:"dispatch_enabled"`
	CreatedAt              time.Time     `json:"created_at,omitempty"`
}
type CapabilitySnapshot struct {
	ID                    int64             `json:"id"`
	OrganizationID        string            `json:"organization_id"`
	ProfileID             string            `json:"profile_id,omitempty"`
	ModelProfileVersionID int64             `json:"model_profile_version_id"`
	Capabilities          []ModelCapability `json:"capabilities"`
	CapabilityHash        string            `json:"capability_hash"`
	CreatedAt             time.Time         `json:"created_at,omitempty"`
}
type RoleBinding struct {
	OrganizationID         string    `json:"organization_id"`
	OrganizationRevisionID int64     `json:"organization_revision_id"`
	RoleID                 string    `json:"role_id"`
	PolicyID               string    `json:"policy_id"`
	ProfileID              string    `json:"profile_id"`
	ModelProfileVersionID  int64     `json:"model_profile_version_id"`
	BindingHash            string    `json:"binding_hash"`
	Active                 bool      `json:"active"`
	CreatedAt              time.Time `json:"created_at,omitempty"`
}

type CreateInvocationCommand struct {
	OrganizationID       string              `json:"organization_id"`
	TaskID               int64               `json:"task_id"`
	AttemptID            int64               `json:"attempt_id"`
	SubjectRoleID        string              `json:"subject_role_id"`
	ContextSnapshotID    int64               `json:"context_snapshot_id"`
	ModelInput           *ModelInputEnvelope `json:"model_input,omitempty"`
	Purpose              string              `json:"purpose"`
	RequiredCapabilities []ModelCapability   `json:"required_capabilities,omitempty"`
	OutputMode           OutputMode          `json:"output_mode"`
	OutputSchema         json.RawMessage     `json:"output_schema,omitempty"`
	MaxOutputTokens      int                 `json:"max_output_tokens"`
	Temperature          *float64            `json:"temperature,omitempty"`
	ThinkingMode         ThinkingMode        `json:"thinking_mode"`
	IdempotencyKey       string              `json:"idempotency_key"`
	CorrelationID        string              `json:"correlation_id,omitempty"`
	CausationID          string              `json:"causation_id,omitempty"`
	Deadline             time.Time           `json:"deadline"`
}

type Invocation struct {
	ID                               int64             `json:"id"`
	OrganizationID                   string            `json:"organization_id"`
	OrganizationRevisionID           int64             `json:"organization_revision_id"`
	TaskID                           int64             `json:"task_id"`
	AttemptID                        int64             `json:"attempt_id"`
	DispatchActorRoleID              string            `json:"dispatch_actor_role_id"`
	SubjectRoleID                    string            `json:"subject_role_id"`
	DispatcherAssignmentID           *int64            `json:"dispatcher_assignment_id,omitempty"`
	ExecutionPrincipalID             *int64            `json:"execution_principal_id,omitempty"`
	ContextSnapshotID                int64             `json:"context_snapshot_id"`
	Purpose                          string            `json:"purpose"`
	ModelProfileID                   string            `json:"model_profile_id"`
	ModelProfileVersionID            int64             `json:"model_profile_version_id"`
	ProviderID                       string            `json:"provider_id"`
	ProviderModelID                  string            `json:"provider_model_id"`
	ModelEgressPolicyVersionID       *int64            `json:"model_egress_policy_version_id,omitempty"`
	ModelEgressPolicyHash            string            `json:"model_egress_policy_hash,omitempty"`
	ExecutionIdentityPolicyVersionID *int64            `json:"execution_identity_policy_version_id,omitempty"`
	ExecutionIdentityPolicyHash      string            `json:"execution_identity_policy_hash,omitempty"`
	RequiredCapabilities             []ModelCapability `json:"required_capabilities"`
	OutputMode                       OutputMode        `json:"output_mode"`
	OutputSchema                     json.RawMessage   `json:"output_schema,omitempty"`
	MaxOutputTokens                  int               `json:"max_output_tokens"`
	Temperature                      *float64          `json:"temperature,omitempty"`
	ThinkingMode                     ThinkingMode      `json:"thinking_mode"`
	IdempotencyKey                   string            `json:"idempotency_key"`
	RequestHash                      string            `json:"request_hash"`
	Status                           InvocationStatus  `json:"status"`
	ErrorCode                        string            `json:"error_code,omitempty"`
	CancelRequestedAt                *time.Time        `json:"cancel_requested_at,omitempty"`
	Deadline                         time.Time         `json:"deadline"`
	CorrelationID                    string            `json:"correlation_id,omitempty"`
	CausationID                      string            `json:"causation_id,omitempty"`
	CreatedAt                        time.Time         `json:"created_at"`
	UpdatedAt                        time.Time         `json:"updated_at"`
	TerminalAt                       *time.Time        `json:"terminal_at,omitempty"`
}

type DispatchAttempt struct {
	ID                     int64          `json:"id"`
	InvocationID           int64          `json:"invocation_id"`
	AttemptNumber          int            `json:"attempt_number"`
	Status                 DispatchStatus `json:"status"`
	ClaimedBy              string         `json:"claimed_by"`
	ExecutionPrincipalID   *int64         `json:"execution_principal_id,omitempty"`
	ExecutionIdentityKeyID *int64         `json:"execution_identity_key_id,omitempty"`
	IdentityAssertionID    *int64         `json:"identity_assertion_id,omitempty"`
	IdentityVerifiedAt     *time.Time     `json:"identity_verified_at,omitempty"`
	ClaimedAt              time.Time      `json:"claimed_at"`
	ClaimExpiresAt         time.Time      `json:"claim_expires_at"`
	SendStartedAt          *time.Time     `json:"send_started_at,omitempty"`
	ResponseReceivedAt     *time.Time     `json:"response_received_at,omitempty"`
	ProviderRequestID      string         `json:"provider_request_id,omitempty"`
	RetrySafety            RetrySafety    `json:"retry_safety"`
	OutcomeClassification  string         `json:"outcome_classification,omitempty"`
	ErrorCode              string         `json:"error_code,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	FinishedAt             *time.Time     `json:"finished_at,omitempty"`
}

type ClaimedInvocation struct {
	Invocation      Invocation      `json:"invocation"`
	DispatchAttempt DispatchAttempt `json:"dispatch_attempt"`
	ClaimToken      string          `json:"-"`
}

type ToolIntent struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type InvocationResult struct {
	ID                int64           `json:"id"`
	InvocationID      int64           `json:"invocation_id"`
	DispatchAttemptID int64           `json:"dispatch_attempt_id"`
	OutputMode        OutputMode      `json:"output_mode"`
	TextOutput        string          `json:"text_output,omitempty"`
	JSONOutput        json.RawMessage `json:"json_output,omitempty"`
	ToolIntents       []ToolIntent    `json:"tool_intents"`
	ResponseHash      string          `json:"response_hash"`
	ResponseBytes     int             `json:"response_bytes"`
	CreatedAt         time.Time       `json:"created_at"`
}
type Usage struct {
	InvocationID      int64 `json:"invocation_id"`
	DispatchAttemptID int64 `json:"dispatch_attempt_id"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	ProviderReported  bool  `json:"provider_reported"`
	// PromptCacheHitTokens/PromptCacheMissTokens are nil whenever the
	// provider omitted the corresponding field — never defaulted to zero,
	// since zero and "not reported" are different facts. Currently
	// populated by the deepseek adapter's DeepSeek-specific
	// prompt_cache_hit_tokens/prompt_cache_miss_tokens usage fields; other
	// providers leave both nil.
	PromptCacheHitTokens  *int64 `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens *int64 `json:"prompt_cache_miss_tokens,omitempty"`
}

type CanonicalRequest struct {
	InvocationID           int64
	DispatchAttemptID      int64
	OrganizationID         string
	OrganizationRevisionID int64
	TaskID                 int64
	AttemptID              int64
	DispatchActorRoleID    string
	SubjectRoleID          string
	ModelProfileID         string
	ModelProfileVersionID  int64
	ProviderID             string
	ProviderModelID        string
	ProviderIdempotencyKey string
	ReasoningEffort        string
	ContextSnapshotID      int64
	ContextRenderedHash    string
	RenderedContext        []byte
	ModelInput             PreparedModelInput
	RequiredCapabilities   []ModelCapability
	OutputMode             OutputMode
	OutputSchema           json.RawMessage
	MaxOutputTokens        int
	Temperature            *float64
	ThinkingMode           ThinkingMode
	Deadline               time.Time
}

type RawToolIntent struct {
	ID        string
	Name      string
	Arguments []byte
}
type RawResponse struct {
	Content               []byte
	ToolIntents           []RawToolIntent
	HiddenReasoning       []byte
	ProviderRequestID     string
	InputTokens           int64
	OutputTokens          int64
	ProviderReported      bool
	CancellationConfirmed bool
	ProviderOutcome       ProviderOutcome
	// PromptCacheHitTokens/PromptCacheMissTokens mirror Usage's fields of
	// the same name — see its doc comment. Set by the deepseek adapter.
	PromptCacheHitTokens  *int64
	PromptCacheMissTokens *int64
}
type NormalizedResponse struct {
	Result                InvocationResult
	Usage                 Usage
	ProviderRequestID     string
	CancellationConfirmed bool
}

type CreateInvocationResult struct {
	Invocation Invocation `json:"invocation"`
	Reused     bool       `json:"reused"`
}
type DispatchResult struct {
	Invocation Invocation        `json:"invocation"`
	Result     *InvocationResult `json:"result,omitempty"`
	Usage      *Usage            `json:"usage,omitempty"`
}
type ReconcileResult struct {
	ReleasedBeforeSend  int `json:"released_before_send"`
	MarkedAmbiguous     int `json:"marked_ambiguous"`
	FailedAfterResponse int `json:"failed_after_response"`
	Inspected           int `json:"inspected"`
}
type CancelResult struct {
	Invocation                   Invocation `json:"invocation"`
	Immediate                    bool       `json:"immediate"`
	AdapterCancellationRequested bool       `json:"adapter_cancellation_requested"`
}
