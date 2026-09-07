package search

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Google Custom Search JSON API provider (V1).
//
// Endpoint: https://www.googleapis.com/customsearch/v1
// Auth:     the key is a required query parameter (`key`) plus the engine id
//           (`cx`). The full request URL is secret-bearing and is never
//           included in errors, logs, metadata or provenance.
// Supports: web_general and news (the API has no news-specific endpoint, so
//           GoogleWeb remains the final fallback for both web routes).
//
// NOTE ON MECHANISM SELECTION: the repository currently has no other Google
// web-search configuration (no Custom Search, no Programmable Search Engine,
// no partially-started adapter), so Google Custom Search JSON API is the
// canonical V1 choice; it is what the ~5000-query allowance maps to.

type GoogleWebProvider struct {
	cfg    WebProviderConfig
	apiKey string
	doer   HTTPDoer
}

func NewGoogleWebProvider(cfg WebProviderConfig, apiKey string, doer HTTPDoer) Provider {
	return &GoogleWebProvider{cfg: cfg, apiKey: apiKey, doer: doer}
}

func (p *GoogleWebProvider) ProviderID() ProviderID { return ProviderGoogleWeb }
func (p *GoogleWebProvider) Name() string           { return "google_web" }
func (p *GoogleWebProvider) Supports(intent SearchIntent) bool {
	return intent == IntentWebGeneral || intent == IntentNews
}

func (p *GoogleWebProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !p.Supports(req.Intent) {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest, StatusCode: 0}
	}
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorUnauthorized, StatusCode: 0}
	}
	if strings.TrimSpace(p.cfg.EngineID) == "" {
		// A configured Google provider without an engine id is a config error:
		// fail closed with a typed error rather than panicking or sending a key
		// to a nonexistent engine.
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest, StatusCode: 0}
	}

	count := req.MaxResults
	if count <= 0 {
		count = 10
	}
	if count > 10 {
		count = 10 // the Custom Search JSON API caps at 10 per request
	}

	endpoint := p.cfg.EndpointURL
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	urlString := endpoint + sep +
		"key=" + url.QueryEscape(p.apiKey) +
		"&cx=" + url.QueryEscape(p.cfg.EngineID) +
		"&q=" + url.QueryEscape(req.Query) +
		"&num=" + strconv.Itoa(count)

	headers := map[string]string{
		"Accept": "application/json",
	}

	body, err := webGet(p.ProviderID(), p.doer, ctx, urlString, headers, p.cfg)
	if err != nil {
		return nil, err
	}

	var decoded googleResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 200, Cause: err}
	}
	if decoded.Error.Code != 0 {
		// The API surfaces auth/rate-limit failures as HTTP 200 with an
		// `error` object; normalize them to typed ProviderErrors.
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: errorKindForStatus(decoded.Error.Code), StatusCode: decoded.Error.Code}
	}

	var out []SearchResult
	for _, item := range decoded.Items {
		title := strings.TrimSpace(item.Title)
		rawurl := strings.TrimSpace(item.Link)
		if title == "" || rawurl == "" {
			continue
		}
		metadata := make(map[string]any)
		if item.FormattedURL != "" {
			metadata["formatted_url"] = item.FormattedURL
		}
		if item.DisplayLink != "" {
			metadata["display_link"] = item.DisplayLink
		}
		out = append(out, SearchResult{
			Provider:     string(ProviderGoogleWeb),
			Title:        title,
			URL:          rawurl,
			Snippet:      strings.TrimSpace(item.Snippet),
			SourceType:   "web",
			CanonicalURL: rawurl,
			Metadata:     metadata,
		})
	}
	return out, nil
}

type googleResponse struct {
	Items []googleItem `json:"items"`
	Error googleError  `json:"error"`
}

type googleItem struct {
	Title        string
	Link         string
	Snippet      string
	FormattedURL string `json:"formattedUrl"`
	DisplayLink  string `json:"displayLink"`
}

type googleError struct {
	Code int
}
