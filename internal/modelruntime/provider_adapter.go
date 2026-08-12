package modelruntime

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var providerMetadataTokenPattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

const (
	ProviderOutcomeResponseReceived = "response_received"
	ProviderOutcomeRejected         = "provider_rejected"
	ProviderOutcomeNotSent          = "request_not_sent"
	ProviderOutcomeAmbiguous        = "ambiguous_transport"
	ProviderOutcomeCancelled        = "cancelled_confirmed"
)

type AdapterDescriptor struct {
	ProviderID            string
	AdapterID             string
	AdapterVersion        int
	Transport             Transport
	RequestSchemaVersion  string
	ResponseSchemaVersion string
	EndpointFingerprint   string
	CredentialRefHash     string
}

func (d AdapterDescriptor) Validate() error {
	if strings.TrimSpace(d.ProviderID) == "" || strings.TrimSpace(d.AdapterID) == "" {
		return fmt.Errorf("%w: adapter provider and ID are required", ErrInvalidRequest)
	}
	if d.AdapterVersion <= 0 {
		return fmt.Errorf("%w: adapter version must be positive", ErrInvalidRequest)
	}
	if d.Transport != TransportHTTP && d.Transport != TransportCLI && d.Transport != TransportFake {
		return fmt.Errorf("%w: unsupported adapter transport", ErrInvalidRequest)
	}
	if strings.TrimSpace(d.RequestSchemaVersion) == "" || strings.TrimSpace(d.ResponseSchemaVersion) == "" {
		return fmt.Errorf("%w: adapter schema versions are required", ErrInvalidRequest)
	}
	if !validProviderSHA256(d.EndpointFingerprint) || !validProviderSHA256(d.CredentialRefHash) {
		return fmt.Errorf("%w: adapter fingerprints must be lowercase SHA-256 values", ErrInvalidRequest)
	}
	return nil
}

type ProviderPreflightRequest struct {
	ProviderID      string
	ProviderModelID string
	Deadline        time.Time
}

type ProviderOutcome struct {
	OutcomeClassification string
	ProviderRequestID     string
	// Transport is optional for rows/events created before CLI adapters existed.
	// An empty transport is interpreted as HTTP for backwards compatibility.
	Transport             Transport
	HTTPStatus            int
	ProcessExitCode       *int
	ErrorClass            string
	ErrorCode             string
	Retryable             bool
	ResponseHash          string
	ResponseSchemaVersion string
	CancellationConfirmed bool

	// --- Gate F: Provider Failure Telemetry ---------------------------------
	// Populated by adapters (currently only deepseek) at every point a
	// ProviderOutcome is constructed, so a failed invocation can be
	// consultably diagnosed without ever persisting the prompt, completion,
	// hidden reasoning, or any secret. Every field below is either a length,
	// a byte count, a provider-supplied enum-like token, or a request-shaping
	// fact that is already public API surface -- never response content.

	// FinishReason is the raw provider finish_reason string (e.g. DeepSeek's
	// chatChoice.FinishReason), whenever the response envelope was decoded
	// far enough to expose one.
	FinishReason string
	// ResponseContentBytes is the length (never the content) of the raw HTTP
	// response body, whenever it was read (even partially, on a bounded-read
	// failure).
	ResponseContentBytes *int
	// UsageAvailable/InputTokens/OutputTokens/CacheHitTokens/CacheMissTokens
	// restate the provider's usage object (if decoded) on the outcome row
	// itself, so a failure row is self-sufficient without joining
	// model_invocation_usage -- which, for genuinely pre-decode failures,
	// has no row for this attempt at all.
	UsageAvailable  bool
	InputTokens     *int64
	OutputTokens    *int64
	CacheHitTokens  *int64
	CacheMissTokens *int64
	// ResponseFormat/MaxOutputTokens echo request-shaping facts (not response
	// content) from the CanonicalRequest that produced this outcome.
	ResponseFormat  string
	MaxOutputTokens *int
	// RequestDuration is wall-clock elapsed time from just before the
	// provider HTTP request was sent to the point this outcome was
	// constructed (success or failure), for latency triage.
	RequestDuration *time.Duration

	// JSONErrorClass/JSONErrorOffset/StartsWithJSONObject/EndsWithJSONObject
	// are populated only for the response_json_invalid failure: the Go
	// encoding/json error's offset and type name (never the JSON body
	// itself), plus two cheap boundary checks that distinguish "provider
	// sent something that isn't JSON at all" from "provider sent JSON that
	// was truncated mid-object".
	JSONErrorClass       string
	JSONErrorOffset      *int64
	StartsWithJSONObject *bool
	EndsWithJSONObject   *bool
}

func (o ProviderOutcome) effectiveTransport() Transport {
	if o.Transport == "" {
		return TransportHTTP
	}
	return o.Transport
}

func (o ProviderOutcome) Validate() error {
	switch o.OutcomeClassification {
	case ProviderOutcomeResponseReceived, ProviderOutcomeRejected, ProviderOutcomeNotSent, ProviderOutcomeAmbiguous, ProviderOutcomeCancelled:
	default:
		return fmt.Errorf("%w: invalid provider outcome classification", ErrInvalidRequest)
	}
	transport := o.effectiveTransport()
	if transport != TransportHTTP && transport != TransportCLI && transport != TransportFake {
		return fmt.Errorf("%w: invalid provider outcome transport", ErrInvalidRequest)
	}
	if o.HTTPStatus < 0 || o.HTTPStatus > 599 {
		return fmt.Errorf("%w: invalid provider HTTP status", ErrInvalidRequest)
	}
	if o.ProcessExitCode != nil && (*o.ProcessExitCode < 0 || *o.ProcessExitCode > 255) {
		return fmt.Errorf("%w: invalid provider process exit code", ErrInvalidRequest)
	}
	if transport == TransportHTTP && o.ProcessExitCode != nil {
		return fmt.Errorf("%w: HTTP outcome cannot carry process exit code", ErrInvalidRequest)
	}
	if transport == TransportCLI && o.HTTPStatus != 0 {
		return fmt.Errorf("%w: CLI outcome cannot carry HTTP status", ErrInvalidRequest)
	}
	if o.ResponseHash != "" && !validProviderSHA256(o.ResponseHash) {
		return fmt.Errorf("%w: invalid provider response hash", ErrInvalidRequest)
	}
	if strings.TrimSpace(o.ResponseSchemaVersion) == "" {
		return fmt.Errorf("%w: provider response schema version is required", ErrInvalidRequest)
	}
	if len(o.ProviderRequestID) > 400 || len(o.ErrorClass) > 120 || len(o.ErrorCode) > 160 || len(o.ResponseSchemaVersion) > 120 {
		return fmt.Errorf("%w: provider outcome metadata exceeds limits", ErrInvalidRequest)
	}
	if o.ProviderRequestID != "" && !validOpaqueProviderRequestID(o.ProviderRequestID) {
		return fmt.Errorf("%w: provider request ID contains unsafe characters", ErrInvalidRequest)
	}
	if o.ErrorClass != "" && !providerMetadataTokenPattern.MatchString(o.ErrorClass) {
		return fmt.Errorf("%w: provider error class is not normalized", ErrInvalidRequest)
	}
	if o.ErrorCode != "" && !providerMetadataTokenPattern.MatchString(o.ErrorCode) {
		return fmt.Errorf("%w: provider error code is not normalized", ErrInvalidRequest)
	}
	if len(o.FinishReason) > 120 || len(o.ResponseFormat) > 60 || len(o.JSONErrorClass) > 120 {
		return fmt.Errorf("%w: provider outcome telemetry metadata exceeds limits", ErrInvalidRequest)
	}
	if o.ResponseContentBytes != nil && *o.ResponseContentBytes < 0 {
		return fmt.Errorf("%w: invalid response content byte length", ErrInvalidRequest)
	}
	if (o.InputTokens != nil && *o.InputTokens < 0) || (o.OutputTokens != nil && *o.OutputTokens < 0) ||
		(o.CacheHitTokens != nil && *o.CacheHitTokens < 0) || (o.CacheMissTokens != nil && *o.CacheMissTokens < 0) {
		return fmt.Errorf("%w: invalid provider outcome token count", ErrInvalidRequest)
	}
	if o.UsageAvailable && o.InputTokens == nil && o.OutputTokens == nil {
		return fmt.Errorf("%w: usage marked available without any recovered token count", ErrInvalidRequest)
	}
	if o.MaxOutputTokens != nil && *o.MaxOutputTokens <= 0 {
		return fmt.Errorf("%w: invalid max output tokens", ErrInvalidRequest)
	}
	if o.RequestDuration != nil && *o.RequestDuration < 0 {
		return fmt.Errorf("%w: invalid request duration", ErrInvalidRequest)
	}
	if o.JSONErrorOffset != nil && *o.JSONErrorOffset < 0 {
		return fmt.Errorf("%w: invalid JSON error offset", ErrInvalidRequest)
	}
	if o.JSONErrorOffset != nil && o.JSONErrorClass == "" {
		return fmt.Errorf("%w: JSON error offset without a JSON error class", ErrInvalidRequest)
	}
	switch o.OutcomeClassification {
	case ProviderOutcomeResponseReceived:
		if o.ResponseHash == "" || o.Retryable || o.CancellationConfirmed {
			return fmt.Errorf("%w: successful provider outcome is inconsistent", ErrInvalidRequest)
		}
		switch transport {
		case TransportHTTP:
			if o.HTTPStatus < 200 || o.HTTPStatus >= 300 {
				return fmt.Errorf("%w: successful HTTP provider outcome is inconsistent", ErrInvalidRequest)
			}
		case TransportCLI:
			if o.ProcessExitCode == nil || *o.ProcessExitCode != 0 {
				return fmt.Errorf("%w: successful CLI provider outcome is inconsistent", ErrInvalidRequest)
			}
		}
	case ProviderOutcomeRejected:
		if o.ResponseHash == "" || strings.TrimSpace(o.ErrorCode) == "" || o.CancellationConfirmed {
			return fmt.Errorf("%w: rejected provider outcome is inconsistent", ErrInvalidRequest)
		}
		if transport == TransportHTTP && o.HTTPStatus < 100 {
			return fmt.Errorf("%w: rejected HTTP provider outcome is inconsistent", ErrInvalidRequest)
		}
		if transport == TransportCLI && o.ProcessExitCode == nil {
			return fmt.Errorf("%w: rejected CLI provider outcome is inconsistent", ErrInvalidRequest)
		}
	case ProviderOutcomeNotSent:
		if o.HTTPStatus != 0 || o.ProcessExitCode != nil || o.ResponseHash != "" || o.ProviderRequestID != "" || strings.TrimSpace(o.ErrorCode) == "" || o.CancellationConfirmed {
			return fmt.Errorf("%w: not-sent provider outcome is inconsistent", ErrInvalidRequest)
		}
	case ProviderOutcomeAmbiguous:
		if o.HTTPStatus != 0 || o.ResponseHash != "" || strings.TrimSpace(o.ErrorCode) == "" || o.CancellationConfirmed {
			return fmt.Errorf("%w: ambiguous provider outcome is inconsistent", ErrInvalidRequest)
		}
	case ProviderOutcomeCancelled:
		if !o.CancellationConfirmed || o.Retryable {
			return fmt.Errorf("%w: cancelled provider outcome is inconsistent", ErrInvalidRequest)
		}
	}
	return nil
}

type AdapterFailurePhase string

const (
	AdapterFailureBeforeRequest    AdapterFailurePhase = "before_request"
	AdapterFailureResponseReceived AdapterFailurePhase = "response_received"
	AdapterFailureAmbiguous        AdapterFailurePhase = "ambiguous_after_request"
)

type AdapterError struct {
	Phase   AdapterFailurePhase
	Outcome ProviderOutcome
	Cause   error
}

func (e *AdapterError) Error() string {
	if e == nil {
		return "model provider adapter error"
	}
	parts := []string{"model provider adapter error", string(e.Phase)}
	if e.Outcome.ErrorClass != "" {
		parts = append(parts, e.Outcome.ErrorClass)
	}
	if e.Outcome.ErrorCode != "" {
		parts = append(parts, e.Outcome.ErrorCode)
	}
	return strings.Join(parts, ": ")
}

func (e *AdapterError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func AsAdapterError(err error) (*AdapterError, bool) {
	var target *AdapterError
	if !errors.As(err, &target) || target == nil {
		return nil, false
	}
	return target, true
}

func validProviderSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validOpaqueProviderRequestID(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}
