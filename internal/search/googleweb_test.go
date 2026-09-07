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

func newGoogleTestProvider(t *testing.T, handler http.HandlerFunc) (Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := WebProviderConfig{
		Enabled:        true,
		EndpointURL:    server.URL + "/customsearch/v1",
		RequestTimeout: 5 * time.Second,
		MaxRetries:     1,
		EngineID:       "0123456789abcdef",
	}
	return NewGoogleWebProvider(cfg, "gtest-key-0000", NewClientHTTPDoer(server.Client())), server
}

func TestGoogle_NormalizesItems(t *testing.T) {
	provider, _ := newGoogleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"items": [
				{"title": "Item One", "link": "https://one.example/1", "snippet": "s1", "formattedUrl": "https://one.example/1", "displayLink": "one.example"},
				{"title": "Item Two", "link": "https://two.example/2", "snippet": "s2", "formattedUrl": "https://two.example/2", "displayLink": "two.example"}
			],
			"searchInformation": {"totalResults": "2"}
		}`))
	})

	results, err := provider.Search(context.Background(), SearchRequest{Query: "test", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 items, got %d", len(results))
	}
	if results[0].Title != "Item One" || results[0].URL != "https://one.example/1" {
		t.Errorf("first item = %q / %q", results[0].Title, results[0].URL)
	}
	if dl, ok := results[0].Metadata["display_link"]; !ok || dl.(string) != "one.example" {
		t.Error("display_link metadata missing or wrong")
	}
}

func TestGoogle_MissingItemsNoPanic(t *testing.T) {
	provider, _ := newGoogleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"searchInformation": {"totalResults": "0"}}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("missing items must not error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestGoogle_AuthConfigError(t *testing.T) {
	provider, _ := newGoogleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error": {"code": 403, "message": "Forbidden"}}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorForbidden {
		t.Errorf("kind = %q, want forbidden", pe.Kind)
	}
}

func TestGoogle_RateLimitInError(t *testing.T) {
	provider, _ := newGoogleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error": {"code": 429, "message": "Quota exceeded"}}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorRateLimited {
		t.Errorf("kind = %q, want rate_limited", pe.Kind)
	}
}

func TestGoogle_ServerError5xx(t *testing.T) {
	var calls int32
	provider, _ := newGoogleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items": [{"title": "ok", "link": "https://ok.example/1"}]}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", atomic.LoadInt32(&calls))
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestGoogle_MalformedJSON(t *testing.T) {
	provider, _ := newGoogleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items": [`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorMalformedResponse {
		t.Errorf("kind = %q, want malformed_response", pe.Kind)
	}
	if strings.Contains(pe.Error(), "gtest-key-0000") {
		t.Error("API key leaked into error")
	}
}

func TestGoogle_MissingEngineIDIsConfigError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called without engine id")
	}))
	t.Cleanup(server.Close)
	cfg := WebProviderConfig{
		Enabled:        true,
		EndpointURL:    server.URL,
		RequestTimeout: 5 * time.Second,
		MaxRetries:     1,
		// EngineID intentionally absent
	}
	provider := NewGoogleWebProvider(cfg, "gtest-key-0000", NewClientHTTPDoer(server.Client()))
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorBadRequest {
		t.Errorf("kind = %q, want bad_request (config error)", pe.Kind)
	}
}
