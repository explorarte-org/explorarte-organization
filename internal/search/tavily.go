package search

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Tavily Search API provider (V1).
//
// Endpoint: https://api.tavily.com/search
// Auth:     the API key travels in the JSON body (Tavily documents
//           { "api_key": ... } in the POST body), never in the URL.
// Supports: web_general and news (Tavily's `topic: "news"` filter).
//
// Tavily can synthesize a single "answer"; the Router needs individual
// evidence sources, so this adapter normalizes only the structured
// `results`/sources list and never substitutes the answer for a SearchResult.

type TavilyProvider struct {
	cfg    WebProviderConfig
	apiKey string
	doer   HTTPDoer
}

func NewTavilyProvider(cfg WebProviderConfig, apiKey string, doer HTTPDoer) Provider {
	return &TavilyProvider{cfg: cfg, apiKey: apiKey, doer: doer}
}

func (p *TavilyProvider) ProviderID() ProviderID { return ProviderTavily }
func (p *TavilyProvider) Name() string           { return "tavily" }
func (p *TavilyProvider) Supports(intent SearchIntent) bool {
	return intent == IntentWebGeneral || intent == IntentNews
}

func (p *TavilyProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !p.Supports(req.Intent) {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest, StatusCode: 0}
	}
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorUnauthorized, StatusCode: 0}
	}

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 20 {
		maxResults = 20
	}

	payload := make(map[string]any)
	payload["api_key"] = p.apiKey
	payload["query"] = req.Query
	payload["search_depth"] = "basic"
	payload["max_results"] = float64(maxResults)
	payload["include_answer"] = false
	payload["include_images"] = false
	if req.Intent == IntentNews {
		payload["topic"] = "news"
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 0, Cause: err}
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	body, err := webPost(p.ProviderID(), p.doer, ctx, p.cfg.EndpointURL, headers, bodyBytes, p.cfg)
	if err != nil {
		return nil, err
	}

	var decoded tavilyResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 200, Cause: err}
	}

	var out []SearchResult
	for _, r := range decoded.Results {
		title := strings.TrimSpace(r.Title)
		rawurl := strings.TrimSpace(r.URL)
		if title == "" || rawurl == "" {
			continue
		}
		metadata := make(map[string]any)
		if r.PublishedDate != "" {
			metadata["published_date"] = r.PublishedDate
		}
		result := SearchResult{
			Provider:   string(ProviderTavily),
			Title:      title,
			URL:        rawurl,
			Snippet:    strings.TrimSpace(r.Content),
			SourceType: "web",
			Score:      r.Score,
			Metadata:   metadata,
		}
		if t, ok := parseTavilyDate(r.PublishedDate); ok {
			result.PublishedAt = &t
		}
		out = append(out, result)
	}
	return out, nil
}

// parseTavilyDate parses Tavily's RFC3339 published_date when present. It
// never fabricates a date: unparsable or empty values yield false.
func parseTavilyDate(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title         string
	URL           string `json:"url"`
	Content       string
	Score         float64
	PublishedDate string `json:"published_date"`
}
