package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("object storage: object not found")
	ErrRequest  = errors.New("object storage: request failed")
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

// PutObject uploads body as objectName with the given content type.
func (c *Client) PutObject(ctx context.Context, objectName string, body []byte, contentType string) error {
	reqURL := c.objectURL(objectName, nil)
	_, _, err := c.do(ctx, http.MethodPut, reqURL, body, contentType)
	return err
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
	return ObjectSummary{Name: objectName, MD5: headers.Get("Content-MD5")}, nil
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

func (c *Client) do(ctx context.Context, method, reqURL string, body []byte, contentType string) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
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
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.Header, fmt.Errorf("%w: %s", ErrNotFound, string(respBody))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("%w: status %d: %s", ErrRequest, resp.StatusCode, string(respBody))
	}
	return respBody, resp.Header, nil
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
