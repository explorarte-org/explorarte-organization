package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTavilyTestProvider(t *testing.T, handler http.HandlerFunc) (Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := WebProviderConfig{
		Enabled:        true,
		EndpointURL:    server.URL + "/search",
		RequestTimeout: 5 * time.Second,
		MaxRetries:     1,
	}
	return NewTavilyProvider(cfg, "tvly-test-key-0000", NewClientHTTPDoer(server.Client())), server
}

func TestTavily_NormalizesResultsAndScore(t *testing.T) {
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("body json: %v", err)
		}
		if api, ok := payload["api_key"]; !ok || api.(string) != "tvly-test-key-0000" {
			t.Error("api_key missing or wrong in body")
		}
		if _, ok := payload["topic"]; ok {
			t.Error("topic should be absent for web_general")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"query": "test",
			"results": [
				{"title": "A", "url": "https://a.example/1", "content": "content a", "score": 0.98, "published_date": "2025-01-02T03:04:05Z"},
				{"title": "B", "url": "https://b.example/2", "content": "content b", "score": 0.80},
				{"title": "C", "url": "https://c.example/3", "content": "content c", "score": 0.61}
			],
			"answer": "synthesized answer",
			"response_time": 0.4
		}`))
	})

	results, err := provider.Search(context.Background(), SearchRequest{Query: "test", Intent: IntentWebGeneral})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Title != "A" || results[0].Score != 0.98 {
		t.Errorf("first result title/score = %q/%v", results[0].Title, results[0].Score)
	}
	if results[0].PublishedAt == nil {
		t.Error("published date should be parsed")
	}
	for _, r := range results {
		if strings.Contains(r.Title, "synthesized answer") || strings.Contains(r.Snippet, "synthesized answer") {
			t.Error("synthesized answer must not become a SearchResult")
		}
	}
}

func TestTavily_NewsUsesTopic(t *testing.T) {
	var gotTopic string
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if v, ok := payload["topic"]; ok {
			gotTopic = v.(string)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results": [{"title": "N", "url": "https://n.example/1", "content": "c", "score": 0.5}]}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "news", Intent: IntentNews})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotTopic != "news" {
		t.Errorf("topic = %q, want news", gotTopic)
	}
}

func TestTavily_Unauthorized(t *testing.T) {
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail": "invalid api key"}`))
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

func TestTavily_RateLimited(t *testing.T) {
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{}`))
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

func TestTavily_InternalServerError(t *testing.T) {
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.StatusCode != 500 {
		t.Errorf("status = %d, want 500", pe.StatusCode)
	}
}

func TestTavily_MalformedJSON(t *testing.T) {
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results": [broken`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Kind != ProviderErrorMalformedResponse {
		t.Errorf("kind = %q, want malformed_response", pe.Kind)
	}
}

func TestTavily_SecretNotInErrors(t *testing.T) {
	provider, _ := newTavilyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"detail": "bad request tvly-test-key-0000"}`))
	})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentWebGeneral})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "tvly-test-key-0000") {
		t.Error("API key leaked into error string")
	}
}
