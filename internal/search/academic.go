package search

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Academic providers (papers). Neither needs a credential: OpenAlex takes an optional API key and a
// contact address for its polite pool; arXiv's export API is open. Like the web adapters, each one
// performs one GET and normalizes the reply; routing, fallback and provenance belong to the Router.

const (
	DefaultOpenAlexEndpoint = "https://api.openalex.org/works"
	DefaultArxivEndpoint    = "https://export.arxiv.org/api/query"
)

// academicMaxResults caps a single request; the scheduler asks for 5.
const academicMaxResults = 25

// OpenAlexProvider searches OpenAlex works.
//
// Endpoint: https://api.openalex.org/works?search=...
// Supports: academic, biomedical, preprint, open_access_resolve (the routes it appears in).
type OpenAlexProvider struct {
	cfg     WebProviderConfig
	apiKey  string
	contact string
	doer    HTTPDoer
	now     func() time.Time
}

func NewOpenAlexProvider(cfg WebProviderConfig, apiKey, contact string, doer HTTPDoer) Provider {
	return &OpenAlexProvider{cfg: cfg, apiKey: apiKey, contact: contact, doer: doer, now: time.Now}
}

func (p *OpenAlexProvider) ProviderID() ProviderID { return ProviderOpenAlex }
func (p *OpenAlexProvider) Name() string           { return "openalex" }
func (p *OpenAlexProvider) Supports(intent SearchIntent) bool {
	switch intent {
	case IntentAcademic, IntentBiomedical, IntentPreprint, IntentOpenAccessResolve:
		return true
	}
	return false
}

func (p *OpenAlexProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !p.Supports(req.Intent) || strings.TrimSpace(req.Query) == "" {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest}
	}
	params := url.Values{}
	params.Set("search", req.Query)
	params.Set("per-page", strconv.Itoa(academicCount(req.MaxResults)))
	// Relevance order over the last year: the worker watches for recent work, and novelty is
	// judged per URL downstream. Sorting by date instead ranks barely related works first.
	params.Set("filter", "has_abstract:true,from_publication_date:"+p.now().AddDate(-1, 0, 0).Format("2006-01-02"))
	if p.contact != "" {
		params.Set("mailto", p.contact)
	}
	if p.apiKey != "" {
		params.Set("api_key", p.apiKey)
	}
	body, err := webGet(p.ProviderID(), p.doer, ctx, joinQuery(p.cfg.EndpointURL, params), map[string]string{"Accept": "application/json"}, p.cfg)
	if err != nil {
		return nil, err
	}
	var decoded openAlexResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 200, Cause: err}
	}
	var out []SearchResult
	for _, work := range decoded.Results {
		title := strings.TrimSpace(work.Title)
		link := work.link()
		if title == "" || link == "" {
			continue
		}
		result := SearchResult{
			Provider:     string(ProviderOpenAlex),
			Title:        title,
			URL:          link,
			CanonicalURL: link,
			DOI:          strings.TrimPrefix(work.DOI, "https://doi.org/"),
			OpenAccess:   work.OpenAccess.IsOA,
			SourceType:   "paper",
			Abstract:     work.abstract(),
			Metadata:     map[string]any{"openalex_id": work.ID, "type": work.Type},
		}
		result.Snippet = snippet(result.Abstract, 400)
		if published, ok := parseDay(work.PublicationDate); ok {
			result.PublishedAt = &published
		}
		for _, authorship := range work.Authorships {
			if name := strings.TrimSpace(authorship.Author.DisplayName); name != "" && len(result.Authors) < 8 {
				result.Authors = append(result.Authors, name)
			}
		}
		if work.PrimaryLocation.Source.DisplayName != "" {
			result.Metadata["venue"] = work.PrimaryLocation.Source.DisplayName
		}
		out = append(out, result)
	}
	return out, nil
}

type openAlexResponse struct {
	Results []openAlexWork `json:"results"`
}

type openAlexWork struct {
	ID              string `json:"id"`
	DOI             string `json:"doi"`
	Title           string `json:"display_name"`
	PublicationDate string `json:"publication_date"`
	Type            string `json:"type"`
	Authorships     []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	AbstractInvertedIndex map[string][]int `json:"abstract_inverted_index"`
	PrimaryLocation       struct {
		LandingPageURL string `json:"landing_page_url"`
		Source         struct {
			DisplayName string `json:"display_name"`
		} `json:"source"`
	} `json:"primary_location"`
	OpenAccess struct {
		IsOA  bool   `json:"is_oa"`
		OAURL string `json:"oa_url"`
	} `json:"open_access"`
}

// link prefers the DOI, then the landing page, then the OpenAlex record itself.
func (w openAlexWork) link() string {
	for _, candidate := range []string{w.DOI, w.PrimaryLocation.LandingPageURL, w.ID} {
		if candidate = strings.TrimSpace(candidate); strings.HasPrefix(candidate, "https://") || strings.HasPrefix(candidate, "http://") {
			return candidate
		}
	}
	return ""
}

// abstract rebuilds the text OpenAlex ships as a word -> positions index.
func (w openAlexWork) abstract() string {
	type placed struct {
		at   int
		word string
	}
	var words []placed
	for word, positions := range w.AbstractInvertedIndex {
		for _, at := range positions {
			words = append(words, placed{at, word})
		}
	}
	sort.Slice(words, func(i, j int) bool { return words[i].at < words[j].at })
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.word
	}
	return strings.Join(out, " ")
}

// ArxivProvider searches the arXiv export API.
//
// Endpoint: https://export.arxiv.org/api/query?search_query=...
// Supports: academic, preprint.
type ArxivProvider struct {
	cfg  WebProviderConfig
	doer HTTPDoer
}

func NewArxivProvider(cfg WebProviderConfig, doer HTTPDoer) Provider {
	return &ArxivProvider{cfg: cfg, doer: doer}
}

func (p *ArxivProvider) ProviderID() ProviderID { return ProviderArxiv }
func (p *ArxivProvider) Name() string           { return "arxiv" }
func (p *ArxivProvider) Supports(intent SearchIntent) bool {
	return intent == IntentAcademic || intent == IntentPreprint
}

func (p *ArxivProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if !p.Supports(req.Intent) {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest}
	}
	query := arxivQuery(req.Query)
	if query == "" {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorBadRequest}
	}
	params := url.Values{}
	params.Set("search_query", query)
	params.Set("start", "0")
	params.Set("max_results", strconv.Itoa(academicCount(req.MaxResults)))
	params.Set("sortBy", "submittedDate")
	params.Set("sortOrder", "descending")
	body, err := webGet(p.ProviderID(), p.doer, ctx, joinQuery(p.cfg.EndpointURL, params), map[string]string{"Accept": "application/atom+xml"}, p.cfg)
	if err != nil {
		return nil, err
	}
	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, &ProviderError{Provider: p.ProviderID(), Kind: ProviderErrorMalformedResponse, StatusCode: 200, Cause: err}
	}
	var out []SearchResult
	for _, entry := range feed.Entries {
		title := collapseSpace(entry.Title)
		link := strings.TrimSpace(entry.ID)
		for _, l := range entry.Links {
			if l.Rel == "alternate" && strings.HasPrefix(l.Href, "http") {
				link = l.Href
			}
		}
		if title == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		abstract := collapseSpace(entry.Summary)
		result := SearchResult{
			Provider:     string(ProviderArxiv),
			Title:        title,
			URL:          link,
			CanonicalURL: link,
			ArxivID:      arxivID(entry.ID),
			DOI:          strings.TrimSpace(entry.DOI),
			OpenAccess:   true,
			SourceType:   "preprint",
			Abstract:     abstract,
			Snippet:      snippet(abstract, 400),
		}
		if published, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Published)); err == nil {
			result.PublishedAt = &published
		}
		for _, author := range entry.Authors {
			if name := strings.TrimSpace(author.Name); name != "" && len(result.Authors) < 8 {
				result.Authors = append(result.Authors, name)
			}
		}
		out = append(out, result)
	}
	return out, nil
}

type arxivFeed struct {
	Entries []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	ID        string `xml:"id"`
	Title     string `xml:"title"`
	Summary   string `xml:"summary"`
	Published string `xml:"published"`
	DOI       string `xml:"http://arxiv.org/schemas/atom doi"`
	Authors   []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

// arxivQuery turns free text into an arXiv field query: every significant term must appear
// (all:a AND all:b ...), capped so a long description does not match nothing.
func arxivQuery(text string) string {
	var terms []string
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r > 127)
	}) {
		if len(word) < 4 || arxivStopwords[word] {
			continue
		}
		terms = append(terms, "all:"+word)
		if len(terms) == 4 {
			break
		}
	}
	return strings.Join(terms, " AND ")
}

var arxivStopwords = map[string]bool{
	"with": true, "from": true, "that": true, "this": true, "into": true, "over": true, "their": true,
	"about": true, "papers": true, "paper": true, "preprints": true, "studies": true, "study": true,
	"latest": true, "research": true, "watch": true, "para": true, "sobre": true, "entre": true,
}

func arxivID(entryID string) string {
	id := strings.TrimSpace(entryID)
	if i := strings.Index(id, "/abs/"); i >= 0 {
		return id[i+len("/abs/"):]
	}
	return ""
}

func academicCount(requested int) int {
	if requested <= 0 {
		return 10
	}
	return min(requested, academicMaxResults)
}

func joinQuery(endpoint string, params url.Values) string {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	return endpoint + sep + params.Encode()
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func snippet(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := strings.LastIndex(s[:limit], " ")
	if cut <= 0 {
		cut = limit
	}
	return s[:cut] + "…"
}

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	return t, err == nil
}

// AcademicProviderSet is the keyless paper providers, built like WebProviderSet.
type AcademicProviderSet struct {
	OpenAlex Provider
	Arxiv    Provider
}

// RegisterInto registers every non-nil provider of the set.
func (s *AcademicProviderSet) RegisterInto(reg *Registry) (int, error) {
	count := 0
	for id, provider := range map[ProviderID]Provider{ProviderOpenAlex: s.OpenAlex, ProviderArxiv: s.Arxiv} {
		if provider == nil {
			continue
		}
		if err := reg.Register(id, provider); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// NewAcademicProviders builds the academic providers enabled in the environment
// (ORG_SEARCH_PROVIDER_OPENALEX_ENABLED, ORG_SEARCH_PROVIDER_ARXIV_ENABLED). OpenAlex also reads an
// optional key (ORG_SEARCH_PROVIDER_OPENALEX_CREDENTIAL_FILE or the openalex-api-key secret) and
// ORG_SEARCH_PROVIDER_OPENALEX_CONTACT, the address its polite pool asks for.
func NewAcademicProviders(lookup LookupEnv, doer HTTPDoer) (*AcademicProviderSet, error) {
	if doer == nil {
		doer = NewClientHTTPDoer(defaultWebClient(20 * time.Second))
	}
	set := &AcademicProviderSet{}
	openAlexEnv := ProviderEnv{Name: "OPENALEX", SecretName: "openalex-api-key"}
	openAlexCfg, err := LoadWebProviderConfig(lookup, openAlexEnv, DefaultOpenAlexEndpoint)
	if err != nil {
		return nil, err
	}
	if openAlexCfg.Enabled {
		key, err := readProviderSecret(openAlexCfg, openAlexEnv)
		if err != nil {
			return nil, err
		}
		contact, _ := lookup("ORG_SEARCH_PROVIDER_OPENALEX_CONTACT")
		set.OpenAlex = NewOpenAlexProvider(openAlexCfg, key, strings.TrimSpace(contact), doer)
	}
	arxivCfg, err := LoadWebProviderConfig(lookup, ProviderEnv{Name: "ARXIV"}, DefaultArxivEndpoint)
	if err != nil {
		return nil, err
	}
	if arxivCfg.Enabled {
		set.Arxiv = NewArxivProvider(arxivCfg, doer)
	}
	return set, nil
}
