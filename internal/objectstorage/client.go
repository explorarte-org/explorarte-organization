package objectstorage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("object storage: object not found")
	ErrRequest  = errors.New("object storage: request failed")

	// ErrPreconditionFailed is returned when a conditional request (an
	// If-None-Match / If-Match precondition) is rejected by the server
	// because the condition does not hold. For PutObjectIfAbsent this
	// means the object already exists.
	ErrPreconditionFailed = errors.New("object storage: precondition failed")

	// ErrImmutableObjectConflict is returned by PutObjectIfAbsent when an
	// object already exists at the target key and its stored digest/size
	// do NOT match the digest/size of the body being written. Evidence and
	// provenance objects in this bucket are immutable: once a key is
	// written, its bytes must never change. Seeing this error means the
	// caller is trying to write different content under a key whose
	// identity scheme promises stability -- that is an upstream
	// correctness bug (e.g. two logically different inputs hashing to the
	// same key), not a transient condition worth retrying as-is.
	ErrImmutableObjectConflict = errors.New("object storage: immutable object conflict: existing object does not match")
)

type Client struct {
	cfg         Config
	httpClient  *http.Client
	signer      *signer
	testBaseURL *url.URL
}

func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, errors.New("object storage: client requested while disabled")
	}
	s, err := newSigner(cfg.KeyID(), cfg.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, httpClient: defaultHTTPClient(), signer: s}, nil
}

type ObjectSummary struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

type listObjectsResponse struct {
	Objects       []ObjectSummary `json:"objects"`
	NextStartWith string          `json:"nextStartWith"`
}

// ListObjects returns every object under prefix, following OCI's
// nextStartWith pagination until the bucket is exhausted.
func (c *Client) ListObjects(ctx context.Context, prefix string) ([]ObjectSummary, error) {
	var all []ObjectSummary
	start := ""
	for {
		query := url.Values{}
		query.Set("fields", "name,size,md5")
		if prefix != "" {
			query.Set("prefix", prefix)
		}
		if start != "" {
			query.Set("start", start)
		}
		reqURL := c.objectsURL(query)
		body, _, err := c.do(ctx, http.MethodGet, reqURL, nil, "")
		if err != nil {
			return nil, err
		}
		var page listObjectsResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode list response: %v", ErrRequest, err)
		}
		all = append(all, page.Objects...)
		if page.NextStartWith == "" {
			return all, nil
		}
		start = page.NextStartWith
	}
}

// GetObject downloads a single object's full content, capped at
// cfg.MaxResponseBytes.
func (c *Client) GetObject(ctx context.Context, objectName string) ([]byte, error) {
	reqURL := c.objectURL(objectName, nil)
	body, _, err := c.do(ctx, http.MethodGet, reqURL, nil, "")
	return body, err
}

// PutObject uploads body as objectName with the given content type. This is
// a plain, unconditional write: it will silently overwrite any existing
// object at objectName. Do NOT use this for evidence/provenance data whose
// keys must be immutable -- use PutObjectIfAbsent instead.
func (c *Client) PutObject(ctx context.Context, objectName string, body []byte, contentType string) error {
	reqURL := c.objectURL(objectName, nil)
	_, _, err := c.do(ctx, http.MethodPut, reqURL, body, contentType)
	return err
}

// PutOutcome describes what PutObjectIfAbsent actually did to reach its
// result.
type PutOutcome int

const (
	// PutOutcomeCreated means objectName did not previously exist and this
	// call created it.
	PutOutcomeCreated PutOutcome = iota
	// PutOutcomeReused means objectName already existed with a digest and
	// size matching the body passed in; nothing was written, and the
	// pre-existing object is the canonical result.
	PutOutcomeReused
)

// PutIfAbsentResult is the result of a successful PutObjectIfAbsent call.
type PutIfAbsentResult struct {
	Outcome PutOutcome
	Object  ObjectSummary
}

// PutObjectIfAbsent atomically creates objectName with body IFF no object
// currently exists at that key. This is a true conditional create, not a
// HEAD-then-PUT check -- HEAD-then-PUT has a TOCTOU race between two
// concurrent writers targeting the same key, which is exactly the failure
// mode immutable evidence storage cannot tolerate.
//
// Atomicity comes from OCI Object Storage's documented If-None-Match
// precondition on PutObject: sending "If-None-Match: *" tells OCI's server
// to accept the write only if no object currently exists under that name,
// and OCI evaluates that condition atomically on its side (this client
// never has to observe intermediate state). If-None-Match is not part of
// the fixed header set OCI's Request Signing Version 1 requires to be
// signed -- the signed set is exactly date, (request-target), host, and
// (for bodies) content-length/content-type/x-content-sha256, see
// signer.go's sign() -- so it rides along as a normal, TLS-protected header
// next to the signed ones, the same way any other unsigned-but-transmitted
// header would. Nothing in this package's client/signer prevents sending
// it, and OCI's PutObject API documents ifNoneMatch as a supported request
// parameter with "*" meaning "only if absent". This has NOT been verified
// against a live OCI endpoint from within this change -- per this
// project's rules, no code or test here makes a real call to OCI -- so this
// is "implemented per OCI's documented contract and exercised against an
// httptest double", not "verified against production". If that documented
// behavior turns out not to hold for this tenancy/bucket, PutObjectIfAbsent
// degrades safely: OCI would either reject the unrecognized precondition
// outright (surfaced as ErrRequest, not silently ignored) or, in the worst
// case neither this client nor its tests can prove one way or the other,
// behave as an unconditional PUT -- callers relying on strict immutability
// should treat that as an operational risk to confirm against the real
// endpoint before depending on this guarantee in production.
//
// When the object already exists (OCI responds 412 Precondition Failed),
// PutObjectIfAbsent reconciles: it fetches the existing object's metadata
// (HeadObject) and, only if that metadata is insufficient to compare (OCI
// omits Content-MD5 for objects written via multipart upload), falls back
// to a single bounded GetObject read -- never unbounded, capped by
// cfg.MaxResponseBytes like every other read in this client. If the
// existing object's digest and size match body: PutOutcomeReused, nil
// error. If they differ: ErrImmutableObjectConflict.
func (c *Client) PutObjectIfAbsent(ctx context.Context, objectName string, body []byte, contentType string) (PutIfAbsentResult, error) {
	reqURL := c.objectURL(objectName, nil)
	_, _, err := c.doRequest(ctx, http.MethodPut, reqURL, body, contentType, http.Header{"If-None-Match": []string{"*"}})
	if err == nil {
		return PutIfAbsentResult{Outcome: PutOutcomeCreated, Object: localSummary(objectName, body)}, nil
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		return PutIfAbsentResult{}, err
	}

	existing, err := c.reconcileExisting(ctx, objectName, body)
	if err != nil {
		return PutIfAbsentResult{}, err
	}
	return PutIfAbsentResult{Outcome: PutOutcomeReused, Object: existing}, nil
}

// reconcileExisting is called after a PutObjectIfAbsent precondition
// conflict (object already exists) to decide REUSE vs
// ErrImmutableObjectConflict.
func (c *Client) reconcileExisting(ctx context.Context, objectName string, body []byte) (ObjectSummary, error) {
	head, err := c.HeadObject(ctx, objectName)
	if err != nil {
		return ObjectSummary{}, fmt.Errorf("object storage: verify existing object %q after precondition conflict: %w", objectName, err)
	}

	if head.MD5 != "" {
		want := localSummary(objectName, body)
		if head.MD5 == want.MD5 && head.Size == want.Size {
			return head, nil
		}
		return ObjectSummary{}, fmt.Errorf("%w: object %q", ErrImmutableObjectConflict, objectName)
	}

	// HeadObject came back without a Content-MD5 (OCI omits it for objects
	// written via multipart upload). Fall back to a single bounded body
	// fetch and compare bytes directly.
	existingBody, err := c.GetObject(ctx, objectName)
	if err != nil {
		return ObjectSummary{}, fmt.Errorf("object storage: fetch existing object %q after precondition conflict: %w", objectName, err)
	}
	if !bytes.Equal(existingBody, body) {
		return ObjectSummary{}, fmt.Errorf("%w: object %q", ErrImmutableObjectConflict, objectName)
	}
	return localSummary(objectName, existingBody), nil
}

// localSummary computes the ObjectSummary for bytes this client already
// holds in memory, using the same base64(MD5) shape OCI reports via
// Content-MD5 so it can be compared directly against HeadObject's result.
func localSummary(objectName string, body []byte) ObjectSummary {
	sum := md5.Sum(body)
	return ObjectSummary{
		Name: objectName,
		Size: int64(len(body)),
		MD5:  base64.StdEncoding.EncodeToString(sum[:]),
	}
}

// DeleteObject removes a single object. OCI Object Storage returns 204 on
// success and treats deleting a nonexistent key as a no-op success (not
// an error) -- this method mirrors that: a delete of something already
// gone is not itself a failure.
func (c *Client) DeleteObject(ctx context.Context, objectName string) error {
	reqURL := c.objectURL(objectName, nil)
	_, _, err := c.do(ctx, http.MethodDelete, reqURL, nil, "")
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// HeadObject checks whether objectName exists without downloading its body.
func (c *Client) HeadObject(ctx context.Context, objectName string) (ObjectSummary, error) {
	reqURL := c.objectURL(objectName, nil)
	_, headers, err := c.do(ctx, http.MethodHead, reqURL, nil, "")
	if err != nil {
		return ObjectSummary{}, err
	}
	summary := ObjectSummary{Name: objectName, MD5: headers.Get("Content-MD5")}
	if contentLength := headers.Get("Content-Length"); contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			summary.Size = size
		}
	}
	return summary, nil
}

func (c *Client) objectsURL(query url.Values) string {
	u := c.baseURL()
	u.Path = fmt.Sprintf("/n/%s/b/%s/o", c.cfg.Namespace, c.cfg.Bucket)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func (c *Client) objectURL(objectName string, query url.Values) string {
	u := c.baseURL()
	u.Path = fmt.Sprintf("/n/%s/b/%s/o/%s", c.cfg.Namespace, c.cfg.Bucket, objectName)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func (c *Client) baseURL() *url.URL {
	if c.testBaseURL != nil {
		clone := *c.testBaseURL
		return &clone
	}
	return c.cfg.BaseURL()
}

// overrideBaseURLForTest points the client at an httptest server instead of
// the real OCI endpoint. Signing still uses cfg.Host() as the Host header
// value would in production; only the transport target changes.
func (c *Client) overrideBaseURLForTest(rawURL string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	c.testBaseURL = parsed
}

// do issues a request with no extra headers beyond what signing itself
// requires. See doRequest for the full implementation.
func (c *Client) do(ctx context.Context, method, reqURL string, body []byte, contentType string) ([]byte, http.Header, error) {
	return c.doRequest(ctx, method, reqURL, body, contentType, nil)
}

// doRequest is the shared request path for every operation in this client.
// extraHeaders, when non-nil, are added to the request before signing (they
// are not part of the signed header set unless signer.sign() already
// includes them by name -- see PutObjectIfAbsent's doc comment for why that
// is fine for If-None-Match specifically).
func (c *Client) doRequest(ctx context.Context, method, reqURL string, body []byte, contentType string, extraHeaders http.Header) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: build request: %v", ErrRequest, err)
	}
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	if body != nil {
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		req.Header.Set("Content-Type", contentType)
	}
	for key, values := range extraHeaders {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	if err := c.signer.sign(req, body); err != nil {
		return nil, nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrRequest, err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, int64(c.cfg.MaxResponseBytes)+1)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read response: %v", ErrRequest, err)
	}
	if len(respBody) > c.cfg.MaxResponseBytes {
		return nil, nil, fmt.Errorf("%w: response exceeds maximum allowed size", ErrRequest)
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, resp.Header, fmt.Errorf("%w: %s", ErrNotFound, sanitizeOCIErrorBody(respBody))
	case http.StatusPreconditionFailed:
		return nil, resp.Header, fmt.Errorf("%w: %s", ErrPreconditionFailed, sanitizeOCIErrorBody(respBody))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("%w: status %d: %s", ErrRequest, resp.StatusCode, sanitizeOCIErrorBody(respBody))
	}
	return respBody, resp.Header, nil
}

// ociErrorBody mirrors OCI Object Storage's standard JSON error envelope.
// Error messages built from HTTP responses surface ONLY these two short,
// well-known fields -- never the raw response body -- since arbitrary OCI
// error payloads are not something this client wants flowing verbatim into
// logs or wrapped errors.
type ociErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const maxSanitizedErrorFieldLen = 200

// sanitizeOCIErrorBody turns a raw HTTP error response body into a short,
// printable diagnostic string safe to embed in a wrapped error or log line.
// It never returns the raw bytes: a recognized {"code","message"} envelope
// is reduced to those two fields (control characters stripped, each
// truncated); anything else collapses to a byte count only.
func sanitizeOCIErrorBody(raw []byte) string {
	var parsed ociErrorBody
	if err := json.Unmarshal(raw, &parsed); err == nil && (parsed.Code != "" || parsed.Message != "") {
		return fmt.Sprintf("code=%s message=%s", sanitizeErrorField(parsed.Code), sanitizeErrorField(parsed.Message))
	}
	return fmt.Sprintf("unreadable error body (%d bytes)", len(raw))
}

func sanitizeErrorField(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxSanitizedErrorFieldLen {
		return s[:maxSanitizedErrorFieldLen] + "...(truncated)"
	}
	return s
}

// DebugRequestNamespace is a temporary diagnostic helper: GET /n/{namespace}
// touches no bucket, so a successful response isolates "signing/auth works"
// from "the bucket/IAM policy is the problem". Not part of the public
// surface used by the ingestion pipeline.
func (c *Client) DebugRequestNamespace(ctx context.Context) ([]byte, http.Header, error) {
	u := c.baseURL()
	u.Path = fmt.Sprintf("/n/%s", c.cfg.Namespace)
	return c.do(ctx, http.MethodGet, u.String(), nil, "")
}
