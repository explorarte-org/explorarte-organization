package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func academicServer(t *testing.T, body string, seen *string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestOpenAlexNormalizesWorksAndRebuildsTheAbstract(t *testing.T) {
	var query string
	server := academicServer(t, `{"results":[
		{"id":"https://openalex.org/W1","doi":"https://doi.org/10.1/abc","display_name":"Rumination and inner speech",
		 "publication_date":"2026-09-01","type":"article",
		 "authorships":[{"author":{"display_name":"A. Author"}}],
		 "abstract_inverted_index":{"speech":[2],"Inner":[0],"regulates":[1]},
		 "primary_location":{"landing_page_url":"https://journal.example/x","source":{"display_name":"J. Psych"}},
		 "open_access":{"is_oa":true}},
		{"id":"not-a-url","display_name":""}]}`, &query)
	cfg := WebProviderConfig{EndpointURL: server.URL, RequestTimeout: 5 * time.Second}
	provider := NewOpenAlexProvider(cfg, "", "research@example.org", NewClientHTTPDoer(server.Client()))
	results, err := provider.Search(context.Background(), SearchRequest{Query: "inner speech", Intent: IntentAcademic, MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the one usable work", results)
	}
	r := results[0]
	if r.URL != "https://doi.org/10.1/abc" || r.DOI != "10.1/abc" || r.Abstract != "Inner regulates speech" || !r.OpenAccess ||
		r.PublishedAt == nil || len(r.Authors) != 1 || r.Metadata["venue"] != "J. Psych" {
		t.Fatalf("normalized %+v", r)
	}
	for _, want := range []string{"search=inner+speech", "per-page=5", "mailto=research%40example.org", "filter=has_abstract%3Atrue%2Cfrom_publication_date%3A"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query %q lacks %q", query, want)
		}
	}
	if provider.Supports(IntentWebGeneral) || provider.Supports(IntentBookGeneral) {
		t.Fatal("OpenAlex must stay out of web and general book search")
	}
}

func TestArxivParsesTheAtomFeed(t *testing.T) {
	var query string
	server := academicServer(t, `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <id>http://arxiv.org/abs/2609.01234v1</id>
    <published>2026-09-20T10:00:00Z</published>
    <title>Metacognitive
      control in language agents</title>
    <summary>  We study   metacognition. </summary>
    <author><name>B. Author</name></author>
    <arxiv:doi>10.2/xyz</arxiv:doi>
    <link href="https://arxiv.org/abs/2609.01234v1" rel="alternate" type="text/html"/>
    <link title="pdf" href="https://arxiv.org/pdf/2609.01234v1" rel="related"/>
  </entry>
</feed>`, &query)
	cfg := WebProviderConfig{EndpointURL: server.URL, RequestTimeout: 5 * time.Second}
	provider := NewArxivProvider(cfg, NewClientHTTPDoer(server.Client()))
	results, err := provider.Search(context.Background(), SearchRequest{Query: "Metacognition and rumination papers", Intent: IntentPreprint, MaxResults: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	r := results[0]
	if r.Title != "Metacognitive control in language agents" || r.URL != "https://arxiv.org/abs/2609.01234v1" ||
		r.ArxivID != "2609.01234v1" || r.DOI != "10.2/xyz" || r.Abstract != "We study metacognition." || r.PublishedAt == nil {
		t.Fatalf("normalized %+v", r)
	}
	if !strings.Contains(query, "search_query=all%3Ametacognition+AND+all%3Arumination") || !strings.Contains(query, "max_results=3") {
		t.Fatalf("query %q", query)
	}
}

func TestArxivQueryKeepsFourSignificantTerms(t *testing.T) {
	got := arxivQuery("Metacognition, rumination, inner speech and mental regulation: latest papers")
	if got != "all:metacognition AND all:rumination AND all:inner AND all:speech" {
		t.Fatalf("arxivQuery = %q", got)
	}
	if arxivQuery("a an of") != "" {
		t.Fatal("a query of stopwords must be empty, not match everything")
	}
}

// The academic providers are opt-in per environment and need no key; a router built with them
// answers the research role's academic intent.
func TestAcademicProvidersRouteThroughARouter(t *testing.T) {
	var query string
	server := academicServer(t, `{"results":[{"id":"https://openalex.org/W9","display_name":"A paper"}]}`, &query)
	env := map[string]string{
		"ORG_SEARCH_PROVIDER_OPENALEX_ENABLED":      "true",
		"ORG_SEARCH_PROVIDER_OPENALEX_ENDPOINT_URL": server.URL,
	}
	set, err := NewAcademicProviders(func(k string) (string, bool) { v, ok := env[k]; return v, ok }, NewClientHTTPDoer(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if set.Arxiv != nil || set.OpenAlex == nil {
		t.Fatalf("set %+v: only the enabled provider is built", set)
	}
	registry := NewRegistry()
	if n, err := set.RegisterInto(registry); err != nil || n != 1 {
		t.Fatalf("registered %d, %v", n, err)
	}
	router, err := NewRouter(RouterConfig{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	response, err := router.Search(context.Background(), SearchRequest{Query: "x", Intent: IntentAcademic, RoleID: string(RoleResearch), MaxResults: 5})
	if err != nil || len(response.Results) != 1 || response.Results[0].Provenance.Provider != string(ProviderOpenAlex) {
		t.Fatalf("response %+v err %v", response, err)
	}
}
