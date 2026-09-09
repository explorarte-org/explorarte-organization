package search

import (
	"strconv"
	"strings"
	"time"
)

// ProviderErrorKind classifies provider failures structurally, so callers and
// the Router never need to string-match an error message to tell a 401 from a
// 429. This is V1's minimal, explicit error taxonomy for external providers.
type ProviderErrorKind string

const (
	ProviderErrorBadRequest        ProviderErrorKind = "bad_request"
	ProviderErrorUnauthorized      ProviderErrorKind = "unauthorized"
	ProviderErrorForbidden         ProviderErrorKind = "forbidden"
	ProviderErrorRateLimited       ProviderErrorKind = "rate_limited"
	ProviderErrorTimeout           ProviderErrorKind = "timeout"
	ProviderErrorUnavailable       ProviderErrorKind = "unavailable"
	ProviderErrorInternal          ProviderErrorKind = "internal"
	ProviderErrorMalformedResponse ProviderErrorKind = "malformed_response"
)

// ProviderError is the structured error returned by the web provider
// adapters. It carries the failure Kind, the HTTP status that triggered it
// (0 for transport failures), an optional Retry-After hint for 429 replies,
// and the original cause. The Error() message is deliberately short and never
// contains API keys, the full request URL, or response bodies.
type ProviderError struct {
	Provider   ProviderID
	Kind       ProviderErrorKind
	StatusCode int
	RetryAfter time.Duration
	Cause      error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "search provider error"
	}
	parts := []string{"search provider", string(e.Provider)}
	if e.Kind != "" {
		parts = append(parts, string(e.Kind))
	}
	if e.StatusCode != 0 {
		parts = append(parts, strconv.Itoa(e.StatusCode))
	}
	return strings.Join(parts, " ")
}

// errorKindForStatus maps an HTTP status to the canonical error kind.
func errorKindForStatus(code int) ProviderErrorKind {
	switch code {
	case 400:
		return ProviderErrorBadRequest
	case 401:
		return ProviderErrorUnauthorized
	case 403:
		return ProviderErrorForbidden
	case 408:
		return ProviderErrorTimeout
	case 429:
		return ProviderErrorRateLimited
	case 503:
		return ProviderErrorUnavailable
	default:
		if code >= 500 {
			return ProviderErrorInternal
		}
		return ProviderErrorUnavailable
	}
}

// retryableStatus reports whether the status is a clearly transient failure
// that the V1 retry policy may retry once: timeout, 408, 429, 500, 502, 503,
// 504. Authentication and validation statuses (400, 401, 403, 404) are never
// retried.
func retryableStatus(code int) bool {
	switch code {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// parseRetryAfter parses a Retry-After header. SerpAPI/Google/Brave/Tavily
// emit it as integer seconds; a non-numeric (HTTP-date) value falls back to
// zero, meaning "unknown", so the caller applies its own bound.
func parseRetryAfter(headerValue string) time.Duration {
	raw := strings.TrimSpace(headerValue)
	if raw == "" {
		return 0
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
