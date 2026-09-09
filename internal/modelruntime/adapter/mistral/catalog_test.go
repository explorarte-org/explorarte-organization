package mistral

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fixedClock gives deterministic TTL behavior.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                 { return c.t }
func (c fixedClock) Add(d time.Duration) fixedClock { return fixedClock{t: c.t.Add(d)} }

func catalogBody(models ...string) string {
	var b strings.Builder
	b.WriteString(`{"data":[`)
	for i, m := range models {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"` + m + `","archived":false,"capabilities":["completion_chat"]}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func testToken(context.Context) (string, error) { return "test-secret", nil }

// upstream counter to prove singleflight (at most one upstream refresh).
var upstreamCalls int64

func newTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&upstreamCalls, 1)
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// catalogTarget rewrites the fixed endpoint host to the test server by
// pointing the catalog at a client whose transport redirects. Instead we
// exercise the real client through an http.RoundTripper swap: the catalog
// is host-fixed to api.mistral.ai, so tests swap the transport to capture
// the request and serve controlled responses.
type captureTransport struct {
	status int
	body   string
	calls  int64
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt64(&c.calls, 1)
	rec := httptest.NewRecorder()
	if req.URL.Host != "api.mistral.ai" || req.URL.Scheme != "https" {
		rec.WriteHeader(http.StatusTeapot)
		return rec.Result(), nil
	}
	if req.URL.Path != "/v1/models" {
		rec.WriteHeader(http.StatusNotFound)
		return rec.Result(), nil
	}
	if req.Header.Get("Authorization") != "Bearer test-secret" {
		rec.WriteHeader(http.StatusUnauthorized)
		return rec.Result(), nil
	}
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(c.status)
	_, _ = io.WriteString(rec, c.body)
	return rec.Result(), nil
}

func newTestCatalog(t *testing.T, tr *captureTransport, now func() time.Time, ttl time.Duration) *Catalog {
	t.Helper()
	c, err := NewCatalog(CatalogConfig{
		TokenLoader: testToken,
		TTL:         ttl,
		Now:         now,
		Client:      &http.Client{Transport: tr},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return c
}

// TestCatalogInitialFetchSuccess: first Snapshot hits upstream once and
// validates the configured model.
func TestCatalogInitialFetchSuccess(t *testing.T) {
	tr := &captureTransport{status: 200, body: catalogBody("ministral-8b-latest", "mistral-large-latest")}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	if err := c.ValidateModel(context.Background(), "ministral-8b-latest"); err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if got := atomic.LoadInt64(&tr.calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

// TestCatalogCacheHit: a second validation inside TTL does not hit upstream.
func TestCatalogCacheHit(t *testing.T) {
	tr := &captureTransport{status: 200, body: catalogBody("ministral-8b-latest")}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	_ = c.ValidateModel(context.Background(), "ministral-8b-latest")
	_ = c.ValidateModel(context.Background(), "ministral-8b-latest")
	if got := atomic.LoadInt64(&tr.calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (cache hit expected)", got)
	}
}

// TestCatalogTTLExpiryRefresh: after TTL the next call refreshes upstream.
func TestCatalogTTLExpiryRefresh(t *testing.T) {
	tr := &captureTransport{status: 200, body: catalogBody("ministral-8b-latest")}
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	c := newTestCatalog(t, tr, func() time.Time { return cur }, DefaultCatalogTTL)
	_ = c.ValidateModel(context.Background(), "ministral-8b-latest")
	cur = base.Add(DefaultCatalogTTL + time.Minute)
	_ = c.ValidateModel(context.Background(), "ministral-8b-latest")
	if got := atomic.LoadInt64(&tr.calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 (refresh after TTL)", got)
	}
}

// TestCatalogConcurrentRefresh: many concurrent first-callers produce at
// most one upstream refresh.
func TestCatalogConcurrentRefresh(t *testing.T) {
	tr := &captureTransport{status: 200, body: catalogBody("ministral-8b-latest")}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	const n = 16
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = c.ValidateModel(context.Background(), "ministral-8b-latest")
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	if got := atomic.LoadInt64(&tr.calls); got > 2 {
		t.Fatalf("upstream calls = %d, want <= 2 (singleflight)", got)
	}
}

// TestCatalogTransientFailureWithValidCache: upstream failure inside TTL
// still serves the cached snapshot.
func TestCatalogTransientFailureWithValidCache(t *testing.T) {
	tr := &captureTransport{status: 200, body: catalogBody("ministral-8b-latest")}
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	c := newTestCatalog(t, tr, func() time.Time { return cur }, DefaultCatalogTTL)
	if err := c.ValidateModel(context.Background(), "ministral-8b-latest"); err != nil {
		t.Fatalf("first: %v", err)
	}
	tr.status, tr.body = 503, "unavailable"
	cur = base.Add(time.Minute)
	if err := c.ValidateModel(context.Background(), "ministral-8b-latest"); err != nil {
		t.Fatalf("cached serve after upstream failure: %v", err)
	}
}

// TestCatalogTransientFailureNoCache: upstream failure with no valid cache
// surfaces ErrCatalogUnavailable.
func TestCatalogTransientFailureNoCache(t *testing.T) {
	tr := &captureTransport{status: 503, body: "unavailable"}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("err = %v, want ErrCatalogUnavailable", err)
	}
}

// TestCatalogUnauthorized: 401 surfaces ErrCatalogUnauthorized.
func TestCatalogUnauthorized(t *testing.T) {
	tr := &captureTransport{status: 401, body: `{"message":"unauthorized"}`}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrCatalogUnauthorized) {
		t.Fatalf("err = %v, want ErrCatalogUnauthorized", err)
	}
}

// hungTransport blocks until the request context is done, proving the
// catalog honors context cancellation/timeout.
type hungTransport struct{}

func (hungTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestCatalogTimeout(t *testing.T) {
	c, err := NewCatalog(CatalogConfig{
		TokenLoader: testToken,
		TTL:         DefaultCatalogTTL,
		Now:         fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}.Now,
		Client:      &http.Client{Transport: hungTransport{}},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Snapshot(ctx); err == nil {
		t.Fatalf("expected timeout error from hung upstream")
	}
}

// TestCatalogMalformedJSON: non-JSON body surfaces ErrCatalogMalformed.
func TestCatalogMalformedJSON(t *testing.T) {
	tr := &captureTransport{status: 200, body: "<html>not json</html>"}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrCatalogMalformed) {
		t.Fatalf("err = %v, want ErrCatalogMalformed", err)
	}
}

// TestCatalogOversized: an oversized body surfaces ErrCatalogOversized.
func TestCatalogOversized(t *testing.T) {
	huge := `{"data":[{"id":"` + strings.Repeat("x", 5<<20) + `"}]}`
	tr := &captureTransport{status: 200, body: huge}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrCatalogOversized) {
		t.Fatalf("err = %v, want ErrCatalogOversized", err)
	}
}

// TestCatalogAbsentModel: a model missing from the catalog is ineligible.
func TestCatalogAbsentModel(t *testing.T) {
	tr := &captureTransport{status: 200, body: catalogBody("mistral-large-latest")}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrModelNotInCatalog) {
		t.Fatalf("err = %v, want ErrModelNotInCatalog", err)
	}
}

// TestCatalogArchivedModel: an archived model is ineligible.
func TestCatalogArchivedModel(t *testing.T) {
	body := `{"data":[{"id":"ministral-8b-latest","archived":true,"capabilities":["completion_chat"]}]}`
	tr := &captureTransport{status: 200, body: body}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrModelArchived) {
		t.Fatalf("err = %v, want ErrModelArchived", err)
	}
}

// TestCatalogCompletionChatFalse: a model without completion_chat is
// ineligible.
func TestCatalogCompletionChatFalse(t *testing.T) {
	body := `{"data":[{"id":"ministral-8b-latest","archived":false,"capabilities":["embedding"]}]}`
	tr := &captureTransport{status: 200, body: body}
	clk := fixedClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newTestCatalog(t, tr, clk.Now, DefaultCatalogTTL)
	err := c.ValidateModel(context.Background(), "ministral-8b-latest")
	if !errors.Is(err, ErrModelCapabilityMissing) {
		t.Fatalf("err = %v, want ErrModelCapabilityMissing", err)
	}
}
