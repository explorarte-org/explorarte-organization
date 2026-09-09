package search

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Brave Web Search API provider (V1).
//
// Endpoint: https://api.search.brave.com/res/v1/web/search
// Auth:     X-Subscription-Token header
// Supports: web_general (the News route is Tavily -> SerpAPI -> GoogleWeb,
//           so Brave deliberately does not advertise IntentNews).
//
// The adapter only performs one HTTP GET and normalizes the reply. Fallback,
// sufficiency, cache, deduplication and provenance decisions belong to the
// Router, never here.

type BraveProvider struct {
	cfg    WebProviderConfig
	apiKey string
	doer   HTTPDoer
}

func NewBraveProvider(cfg WebProviderConfig, apiKey string, doer HTTPDoer) Provider {
	return &BraveProvider{cfg: cfg, apiKey: apiKey, doer: doer}
}

func (p *BraveProvider) ProviderID() ProviderID { return ProviderBrave }
func (p *BraveProvider) Name() string           { return "brave_web" }
func (p *BraveProvider) Supports(intent SearchIntent) bool {
	return intent == IntentWebGeneral
}

func (p *BraveProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !p.Supports(req.Intent) {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest, StatusCode: 0}
	}
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorUnauthorized, StatusCode: 0}
	}

	endpoint := p.cfg.EndpointURL
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	count := req.MaxResults
	if count <= 0 {
		count = 10
	}
	if count > 20 {
		count = 20 // Brave caps count at 20
	}
	urlString := endpoint + sep +
		"q=" + url.QueryEscape(req.Query) +
		"&count=" + strconv.Itoa(count) +
		"&source=web"

	headers := map[string]string{
		"Accept":               "application/json",
		"X-Subscription-Token": p.apiKey,
	}

	body, err := webGet(p.ProviderID(), p.doer, ctx, urlString, headers, p.cfg)
	if err != nil {
		return nil, err
	}

	var decoded braveResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 200, Cause: err}
	}

	var out []SearchResult
	for _, r := range decoded.Web.Results {
		title := strings.TrimSpace(r.Title)
		rawurl := strings.TrimSpace(r.URL)
		if title == "" || rawurl == "" {
			continue
		}
		metadata := make(map[string]any)
		if r.Age != "" {
			metadata["age"] = r.Age
		}
		if r.PageAge != "" {
			metadata["page_age"] = r.PageAge
		}
		if len(r.ExtraSnippets) > 0 {
			metadata["extra_snippets"] = r.ExtraSnippets
		}
		out = append(out, SearchResult{
			Provider:     string(ProviderBrave),
			Title:        title,
			URL:          rawurl,
			Snippet:      strings.TrimSpace(r.Description),
			SourceType:   "web",
			CanonicalURL: rawurl,
			Metadata:     metadata,
		})
	}
	return out, nil
}

type braveResponse struct {
	Web braveWebBlock `json:"web"`
}

type braveWebBlock struct {
	Results []braveResult `json:"results"`
}

type braveResult struct {
	Title         string
	URL           string `json:"url"`
	Description   string
	Age           string
	PageAge       string   `json:"page_age"`
	ExtraSnippets []string `json:"extra_snippets"`
}
