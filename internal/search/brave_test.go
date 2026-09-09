package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newBraveTestProvider(t *testing.T, handler http.HandlerFunc) (Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := WebProviderConfig{
		Enabled:        true,
		EndpointURL:    server.URL + "/res/v1/web/search",
		RequestTimeout: 5 * time.Second,
		MaxRetries:     1,
	}
	return NewBraveProvider(cfg, "brave-test-key-0000", NewClientHTTPDoer(server.Client())), server
}

func TestBrave_ParsesResults(t *testing.T) {
	var gotHeader string
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Subscription-Token")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"web": {
				"results": [
					{"title": "First Result", "url": "https://example.com/1", "description": "desc one", "age": "1 day", "page_age": "1 day"},
					{"title": "Second Result", "url": "https://example.org/2", "description": "desc two"},
					{"title": "Third Result", "url": "https://example.net/3", "description": "desc three"}
				]
			}
		}`))
	})

	results, err := provider.Search(context.Background(), SearchRequest{Query: "test", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Title != "First Result" {
		t.Errorf("title = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("url = %q", results[0].URL)
	}
	if results[0].Snippet != "desc one" {
		t.Errorf("snippet = %q", results[0].Snippet)
	}
	if gotHeader != "brave-test-key-0000" {
		t.Errorf("X-Subscription-Token = %q", gotHeader)
	}
}

func TestBrave_EmptyResults(t *testing.T) {
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"web": {"results": []}}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestBrave_MalformedJSON(t *testing.T) {
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"web": {"results": [not-json]}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorMalformedResponse {
		t.Errorf("kind = %q, want malformed_response", pe.Kind)
	}
	if strings.Contains(pe.Error(), "brave-test-key-0000") {
		t.Error("API key leaked into error")
	}
}

func TestBrave_Unauthorized(t *testing.T) {
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorUnauthorized {
		t.Errorf("kind = %q, want unauthorized", pe.Kind)
	}
}

func TestBrave_RateLimitedWithRetryAfter(t *testing.T) {
	var calls int32
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"web": {"results": [{"title": "ok", "url": "https://example.com/ok", "description": "d"}]}}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorRateLimited {
		t.Errorf("kind = %q, want rate_limited", pe.Kind)
	}
	if pe.RetryAfter != 60*time.Second {
		t.Errorf("retry_after = %v, want 60s", pe.RetryAfter)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected no retry for long Retry-After, got %d calls", atomic.LoadInt32(&calls))
	}
}

func TestBrave_RetriesTransient500(t *testing.T) {
	var calls int32
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"web": {"results": [{"title": "ok", "url": "https://example.com/ok", "description": "d"}]}}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("expected success after one retry, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", atomic.LoadInt32(&calls))
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestBrave_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	cfg := WebProviderConfig{
		Enabled:        true,
		EndpointURL:    server.URL,
		RequestTimeout: 50 * time.Millisecond,
		MaxRetries:     0,
	}
	provider := NewBraveProvider(cfg, "k", NewClientHTTPDoer(server.Client()))
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorTimeout {
		t.Errorf("kind = %q, want timeout", pe.Kind)
	}
}

func TestBrave_SecretNotInResults(t *testing.T) {
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"web": {"results": [{"title": "t", "url": "https://example.com/1", "description": "d"}]}}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "brave-test-key-0000", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if strings.Contains(r.Title, "brave-test-key") || strings.Contains(r.URL, "brave-test-key") || strings.Contains(r.Snippet, "brave-test-key") {
			t.Error("API key leaked into result fields")
		}
	}
}

func TestBrave_DoesNotSupportNews(t *testing.T) {
	provider, _ := newBraveTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called for news intent")
	})
	if provider.Supports(IntentNews) {
		t.Error("Brave should not support IntentNews")
	}
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentNews})
	if err == nil {
		t.Error("expected error for unsupported intent")
	}
}
