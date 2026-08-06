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
	HTTPStatus            int
	ErrorClass            string
	ErrorCode             string
	Retryable             bool
	ResponseHash          string
	ResponseSchemaVersion string
	CancellationConfirmed bool
}

func (o ProviderOutcome) Validate() error {
	switch o.OutcomeClassification {
	case ProviderOutcomeResponseReceived, ProviderOutcomeRejected, ProviderOutcomeNotSent, ProviderOutcomeAmbiguous, ProviderOutcomeCancelled:
	default:
		return fmt.Errorf("%w: invalid provider outcome classification", ErrInvalidRequest)
	}
	if o.HTTPStatus < 0 || o.HTTPStatus > 599 {
		return fmt.Errorf("%w: invalid provider HTTP status", ErrInvalidRequest)
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
	switch o.OutcomeClassification {
	case ProviderOutcomeResponseReceived:
		if o.HTTPStatus < 200 || o.HTTPStatus >= 300 || o.ResponseHash == "" || o.Retryable || o.CancellationConfirmed {
			return fmt.Errorf("%w: successful provider outcome is inconsistent", ErrInvalidRequest)
		}
	case ProviderOutcomeRejected:
		if o.HTTPStatus < 100 || o.ResponseHash == "" || strings.TrimSpace(o.ErrorCode) == "" || o.CancellationConfirmed {
			return fmt.Errorf("%w: rejected provider outcome is inconsistent", ErrInvalidRequest)
		}
	case ProviderOutcomeNotSent:
		if o.HTTPStatus != 0 || o.ResponseHash != "" || o.ProviderRequestID != "" || strings.TrimSpace(o.ErrorCode) == "" || o.CancellationConfirmed {
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
