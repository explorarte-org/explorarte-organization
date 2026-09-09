package search

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// =============================================================================
// HTTP layer for web providers (V1)
//
// Providers never touch http.DefaultClient directly. Each adapter accepts an
// HTTPDoer -- a narrow injection point -- so unit tests can either point at an
// httptest server or supply a fake without any external network. The default
// ClientHTTPDoer wraps the standard *http.Client the org's other HTTP adapters
// build (proxy/TLS/timeouts).
// =============================================================================

// HTTPDoer is the narrow HTTP boundary the web providers depend on.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type ClientHTTPDoer struct {
	client *http.Client
}

func (d *ClientHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return d.client.Do(req)
}

// NewClientHTTPDoer wraps a concrete *http.Client (e.g. httptest's
// server.Client() in tests, or defaultWebClient() in production).
func NewClientHTTPDoer(client *http.Client) HTTPDoer {
	return &ClientHTTPDoer{client: client}
}

// defaultWebClient builds a concrete *http.Client for outbound production
// use: TLS >= 1.2, a bounded connect timeout, and redirects turned off so a
// redirecting provider cannot smuggle the API key to another host silently.
func defaultWebClient(requestTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		// ResponseHeaderTimeout bounds the wait for the first response byte;
		// the request-level timeout (context.WithTimeout) is the total bound.
		ResponseHeaderTimeout: requestTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("search web provider redirects are forbidden")
		},
	}
}

// readBounded reads at most limit bytes from the response body, refusing
// oversized replies so a misbehaving provider cannot exhaust memory.
func readBounded(r io.Reader, limit int) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return body, err
	}
	if len(body) > limit {
		return body[:limit], errors.New("search web provider response exceeds byte bound")
	}
	return body, nil
}

// webGet performs a single GET through the doer, applying the V1 retry policy
// (at most one retry) for clearly transient failures, and returning the raw
// body on success. On failure it returns (nil, *ProviderError). The request
// URL is never placed in error messages, so an api_key carried as a query
// parameter cannot leak into errors/logs.
func webGet(provider ProviderID, doer HTTPDoer, ctx context.Context, urlString string, headers map[string]string, cfg WebProviderConfig) ([]byte, error) {
	return webRequest(provider, doer, ctx, http.MethodGet, urlString, headers, nil, cfg)
}

// webPost performs a single POST with the given body through the doer,
// applying the same V1 retry policy as webGet.
func webPost(provider ProviderID, doer HTTPDoer, ctx context.Context, urlString string, headers map[string]string, body []byte, cfg WebProviderConfig) ([]byte, error) {
	return webRequest(provider, doer, ctx, http.MethodPost, urlString, headers, body, cfg)
}

// webRequest is the shared retry loop for a single HTTP request.
func webRequest(provider ProviderID, doer HTTPDoer, ctx context.Context, method, urlString string, headers map[string]string, body []byte, cfg WebProviderConfig) ([]byte, error) {
	attempts := 0
	for {
		bodyOut, err := webRequestOnce(provider, doer, ctx, method, urlString, headers, body, cfg.RequestTimeout)
		if err == nil {
			return bodyOut, nil
		}
		if !retryableWebError(err) || attempts >= cfg.MaxRetries {
			return nil, err
		}
		attempts++
	}
}

// retryableWebError encodes the V1 policy: retry transient transport
// timeouts, 408, 429, 500, 502, 503, 504. Never retry 400/401/403/404, and
// never retry a 429 whose Retry-After exceeds the bounded sleep cap -- the
// worker must not block for minutes inside one request.
func retryableWebError(err error) bool {
	pe, ok := err.(*ProviderError)
	if !ok {
		return false
	}
	if pe.Kind == ProviderErrorTimeout {
		return true
	}
	if pe.StatusCode == 429 {
		// Parse Retry-After; a long or unknown delay is not worth a sleep:
		// return the typed error and let the Router fall back.
		if pe.RetryAfter == 0 || pe.RetryAfter > 2*time.Second {
			return false
		}
		return true
	}
	return retryableStatus(pe.StatusCode)
}

// webRequestOnce performs exactly one request and maps the outcome: transport
// errors into a ProviderError, non-2xx into a ProviderError carrying the
// parsed status and Retry-After, and 2xx into the bounded response body.
func webRequestOnce(provider ProviderID, doer HTTPDoer, ctx context.Context, method, urlString string, headers map[string]string, body []byte, timeout time.Duration) ([]byte, error) {
	requestCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	bodyReader := bytes.NewReader(body)
	if body == nil {
		bodyReader = bytes.NewReader([]byte{})
	}
	req, err := http.NewRequestWithContext(requestCtx, method, urlString, bodyReader)
	if err != nil {
		return nil, &ProviderError{Provider: provider, Kind: ProviderErrorMalformedResponse, StatusCode: 0, Cause: err}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return nil, providerTransportError(provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ProviderError{
			Provider:   provider,
			Kind:       errorKindForStatus(resp.StatusCode),
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	out, err := readBounded(resp.Body, 4<<20)
	if err != nil {
		return nil, &ProviderError{Provider: provider, Kind: ProviderErrorMalformedResponse, StatusCode: resp.StatusCode, Cause: err}
	}
	return out, nil
}

// providerTransportError classifies a client.Do failure: timeouts get their
// own Kind so retry/fallback logic can distinguish "network stalled" from
// "provider answered".
func providerTransportError(provider ProviderID, cause error) error {
	kind := ProviderErrorUnavailable
	if e, ok := cause.(*url.Error); ok && e.Timeout() {
		kind = ProviderErrorTimeout
	}
	return &ProviderError{Provider: provider, Kind: kind, StatusCode: 0, Cause: cause}
}
