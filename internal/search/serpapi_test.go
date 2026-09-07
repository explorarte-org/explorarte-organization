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

func newSerpapiTestProvider(t *testing.T, handler http.HandlerFunc) (Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := WebProviderConfig{
		Enabled:        true,
		EndpointURL:    server.URL + "/search",
		RequestTimeout: 5 * time.Second,
		MaxRetries:     1,
	}
	return NewSerpapiProvider(cfg, "serp-test-key-0000", NewClientHTTPDoer(server.Client())), server
}

func TestSerpapi_NormalizesOrganicResults(t *testing.T) {
	provider, _ := newSerpapiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RequestURI(), "brave") || strings.Contains(r.URL.RequestURI(), "openalex") {
			t.Error("SerpAPI must not route through another provider")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"organic_results": [
				{"position": 1, "title": "Org One", "link": "https://one.example/1", "snippet": "s1", "displayed_link": "one.example"},
				{"position": 2, "title": "Org Two", "link": "https://two.example/2", "snippet": "s2", "displayed_link": "two.example"},
				{"position": 3, "title": "Org Three", "link": "https://three.example/3", "snippet": "s3", "displayed_link": "three.example"}
			],
			"ads": [
				{"position": 1, "title": "Ad", "link": "https://ad.example/buy", "snippet": "buy now"}
			]
		}`))
	})

	results, err := provider.Search(context.Background(), SearchRequest{Query: "test", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 organic results (ads ignored), got %d", len(results))
	}
	if results[0].Title != "Org One" || results[0].URL != "https://one.example/1" {
		t.Errorf("first result = %q / %q", results[0].Title, results[0].URL)
	}
	if pos, ok := results[0].Metadata["position"]; !ok || pos != 1 {
		t.Error("position metadata missing or wrong")
	}
	for _, r := range results {
		if strings.Contains(strings.ToLower(r.Title), "ad") && strings.Contains(r.URL, "ad.example") {
			t.Error("sponsored result leaked into results")
		}
	}
}

func TestSerpapi_ResultWithoutSnippetTolerated(t *testing.T) {
	provider, _ := newSerpapiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"organic_results": [{"position": 1, "title": "No Snippet", "link": "https://x.example/1"}]}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "No Snippet" || results[0].Snippet != "" {
		t.Errorf("title/snippet = %q/%q", results[0].Title, results[0].Snippet)
	}
}

func TestSerpapi_UnauthorizedForbidden(t *testing.T) {
	provider, _ := newSerpapiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{}`))
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

func TestSerpapi_RateLimit(t *testing.T) {
	var calls int32
	provider, _ := newSerpapiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"organic_results": [{"title": "ok", "link": "https://ok.example/1"}]}`))
	})
	results, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("expected success after short Retry-After retry, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (short Retry-After retry), got %d", atomic.LoadInt32(&calls))
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSerpapi_MalformedJSON(t *testing.T) {
	provider, _ := newSerpapiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"organic_results": [`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorMalformedResponse {
		t.Errorf("kind = %q, want malformed_response", pe.Kind)
	}
	if strings.Contains(pe.Error(), "serp-test-key-0000") {
		t.Error("API key leaked into error")
	}
}

func TestSerpapi_NewsUsesGoogleNewsEngine(t *testing.T) {
	var gotEngine string
	provider, _ := newSerpapiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if v, ok := q["engine"]; ok {
			gotEngine = string(v[0])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"organic_results": []}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "news", Intent: IntentNews})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotEngine != "google_news" {
		t.Errorf("engine = %q, want google_news", gotEngine)
	}
}
