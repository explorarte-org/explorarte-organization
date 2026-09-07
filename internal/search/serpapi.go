package search

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// SerpAPI provider (V1).
//
// Endpoint: https://serpapi.com/search (engine=google)
// Auth:     the API key is required as the `api_key` query parameter; the
//           full request URL is therefore a secret-bearing value and is never
//           included in errors, logs, metadata or provenance. webGet already
//           keeps URLs out of ProviderError messages.
// Supports: web_general and news (engine=google_news).
//
// Only organic results are normalized. Ads/sponsored results and knowledge
// graph entries are ignored because they are not standalone web evidence.

type SerpapiProvider struct {
	cfg    WebProviderConfig
	apiKey string
	doer   HTTPDoer
}

func NewSerpapiProvider(cfg WebProviderConfig, apiKey string, doer HTTPDoer) Provider {
	return &SerpapiProvider{cfg: cfg, apiKey: apiKey, doer: doer}
}

func (p *SerpapiProvider) ProviderID() ProviderID { return ProviderSerpapi }
func (p *SerpapiProvider) Name() string           { return "serpapi" }
func (p *SerpapiProvider) Supports(intent SearchIntent) bool {
	return intent == IntentWebGeneral || intent == IntentNews
}

func (p *SerpapiProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !p.Supports(req.Intent) {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest, StatusCode: 0}
	}
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorUnauthorized, StatusCode: 0}
	}

	count := req.MaxResults
	if count <= 0 {
		count = 10
	}
	if count > 20 {
		count = 20
	}

	engine := "google"
	if req.Intent == IntentNews {
		engine = "google_news"
	}

	endpoint := p.cfg.EndpointURL
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	urlString := endpoint + sep +
		"engine=" + url.QueryEscape(engine) +
		"&q=" + url.QueryEscape(req.Query) +
		"&num=" + strconv.Itoa(count) +
		"&api_key=" + url.QueryEscape(p.apiKey)

	headers := map[string]string{
		"Accept": "application/json",
	}

	body, err := webGet(p.ProviderID(), p.doer, ctx, urlString, headers, p.cfg)
	if err != nil {
		return nil, err
	}

	var decoded serpapiResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 200, Cause: err}
	}

	var out []SearchResult
	for _, r := range decoded.OrganicResults {
		title := strings.TrimSpace(r.Title)
		rawurl := strings.TrimSpace(r.Link)
		if title == "" || rawurl == "" {
			continue
		}
		metadata := make(map[string]any)
		if r.Position > 0 {
			metadata["position"] = r.Position
		}
		if r.DisplayedLink != "" {
			metadata["displayed_link"] = r.DisplayedLink
		}
		out = append(out, SearchResult{
			Provider:     string(ProviderSerpapi),
			Title:        title,
			URL:          rawurl,
			Snippet:      strings.TrimSpace(r.Snippet),
			SourceType:   "web",
			CanonicalURL: rawurl,
			Metadata:     metadata,
		})
	}
	return out, nil
}

type serpapiResponse struct {
	OrganicResults []serpapiResult `json:"organic_results"`
}

type serpapiResult struct {
	Position      int
	Title         string
	Link          string
	Snippet       string
	DisplayedLink string `json:"displayed_link"`
}
