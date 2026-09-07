package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Router<>web-adapter integration tests (CASE A-G).
//
// Every scenario drives the REAL Brave/Tavily/SerpAPI/Google probes against
// per-provider httptest servers, so these are end-to-end within the machine:
// routing order, fallback, cache, usage and observability are all exercised
// with no external network and no real credentials.
// =============================================================================

func webCfg(endpoint string) WebProviderConfig {
	// Retry policy is covered by the per-provider unit tests; routing
	// integration wants deterministic provider-level call counts, so the
	// Router-driver here disables in-adapter retries.
	return WebProviderConfig{
		Enabled:        true,
		EndpointURL:    endpoint,
		RequestTimeout: 5 * time.Second,
		MaxRetries:     0,
	}
}

func webCfgGoogle(endpoint string) WebProviderConfig {
	cfg := webCfg(endpoint)
	cfg.EngineID = "0123456789abcdef"
	return cfg
}

type webCallCounter struct {
	mu      sync.Mutex
	brave   int32
	tavily  int32
	serpapi int32
	google  int32
}

func (c *webCallCounter) Hit(pid ProviderID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch pid {
	case ProviderBrave:
		atomic.AddInt32(&c.brave, 1)
	case ProviderTavily:
		atomic.AddInt32(&c.tavily, 1)
	case ProviderSerpapi:
		atomic.AddInt32(&c.serpapi, 1)
	case ProviderGoogleWeb:
		atomic.AddInt32(&c.google, 1)
	}
}

func (c *webCallCounter) Get(pid ProviderID) int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch pid {
	case ProviderBrave:
		return atomic.LoadInt32(&c.brave)
	case ProviderTavily:
		return atomic.LoadInt32(&c.tavily)
	case ProviderSerpapi:
		return atomic.LoadInt32(&c.serpapi)
	case ProviderGoogleWeb:
		return atomic.LoadInt32(&c.google)
	}
	return 0
}

func webHandlerJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}
}

func braveResultsJSON(n int) string {
	var items []string
	for i := 0; i < n; i++ {
		items = append(items, `{"title": "B`+strconv.Itoa(i)+`", "url": "https://b`+strconv.Itoa(i)+`.example/1", "description": "d"}`)
	}
	return `{"web": {"results": [` + strings.Join(items, ",") + `]}}`
}

func tavilyResultsJSON(n int) string {
	var items []string
	for i := 0; i < n; i++ {
		items = append(items, `{"title": "T`+strconv.Itoa(i)+`", "url": "https://t`+strconv.Itoa(i)+`.example/1", "content": "c", "score": 0.9}`)
	}
	return `{"results": [` + strings.Join(items, ",") + `]}`
}

func serpapiResultsJSON(n int) string {
	var items []string
	for i := 0; i < n; i++ {
		items = append(items, `{"title": "S`+strconv.Itoa(i)+`", "link": "https://s`+strconv.Itoa(i)+`.example/1", "snippet": "s", "position": `+strconv.Itoa(i+1)+`}`)
	}
	return `{"organic_results": [` + strings.Join(items, ",") + `]}`
}

func googleResultsJSON(n int) string {
	var items []string
	for i := 0; i < n; i++ {
		items = append(items, `{"title": "G`+strconv.Itoa(i)+`", "link": "https://g`+strconv.Itoa(i)+`.example/1", "snippet": "s"}`)
	}
	return `{"items": [` + strings.Join(items, ",") + `]}`
}

func TestWebRouter_CaseA_BraveSufficient_OthersNotCalled(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.Write([]byte(braveResultsJSON(3)))
	}))
	t.Cleanup(braveServer.Close)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderTavily)
		w.Write([]byte(tavilyResultsJSON(3)))
	}))
	t.Cleanup(tavilyServer.Close)
	serpapiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderSerpapi)
		w.Write([]byte(serpapiResultsJSON(3)))
	}))
	t.Cleanup(serpapiServer.Close)
	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderGoogleWeb)
		w.Write([]byte(googleResultsJSON(3)))
	}))
	t.Cleanup(googleServer.Close)

	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k-brave", NewClientHTTPDoer(braveServer.Client())))
	reg.Register(ProviderTavily, NewTavilyProvider(webCfg(tavilyServer.URL), "k-tavily", NewClientHTTPDoer(tavilyServer.Client())))
	reg.Register(ProviderSerpapi, NewSerpapiProvider(webCfg(serpapiServer.URL), "k-serpapi", NewClientHTTPDoer(serpapiServer.Client())))
	reg.Register(ProviderGoogleWeb, NewGoogleWebProvider(webCfgGoogle(googleServer.URL), "k-google", NewClientHTTPDoer(googleServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       NewMemoryCache(),
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	_, err := router.Search(context.Background(), SearchRequest{
		Query:      "case a",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if counter.Get(ProviderBrave) != 1 {
		t.Errorf("CASE A Brave calls = %d, want 1", counter.Get(ProviderBrave))
	}
	if counter.Get(ProviderTavily) != 0 {
		t.Errorf("CASE A Tavily calls = %d, want 0", counter.Get(ProviderTavily))
	}
	if counter.Get(ProviderSerpapi) != 0 {
		t.Errorf("CASE A SerpAPI calls = %d, want 0", counter.Get(ProviderSerpapi))
	}
	if counter.Get(ProviderGoogleWeb) != 0 {
		t.Errorf("CASE A Google calls = %d, want 0", counter.Get(ProviderGoogleWeb))
	}
}

func TestWebRouter_CaseB_Brave500_TavilySufficient(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(braveServer.Close)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderTavily)
		w.Write([]byte(tavilyResultsJSON(3)))
	}))
	t.Cleanup(tavilyServer.Close)
	serpapiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderSerpapi)
		w.Write([]byte(serpapiResultsJSON(3)))
	}))
	t.Cleanup(serpapiServer.Close)
	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderGoogleWeb)
		w.Write([]byte(googleResultsJSON(3)))
	}))
	t.Cleanup(googleServer.Close)

	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k", NewClientHTTPDoer(braveServer.Client())))
	reg.Register(ProviderTavily, NewTavilyProvider(webCfg(tavilyServer.URL), "k", NewClientHTTPDoer(tavilyServer.Client())))
	reg.Register(ProviderSerpapi, NewSerpapiProvider(webCfg(serpapiServer.URL), "k", NewClientHTTPDoer(serpapiServer.Client())))
	reg.Register(ProviderGoogleWeb, NewGoogleWebProvider(webCfgGoogle(googleServer.URL), "k", NewClientHTTPDoer(googleServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       NewMemoryCache(),
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	_, err := router.Search(context.Background(), SearchRequest{
		Query:      "case b",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if counter.Get(ProviderBrave) != 1 {
		t.Errorf("CASE B Brave calls = %d, want 1", counter.Get(ProviderBrave))
	}
	if counter.Get(ProviderTavily) != 1 {
		t.Errorf("CASE B Tavily calls = %d, want 1", counter.Get(ProviderTavily))
	}
	if counter.Get(ProviderSerpapi) != 0 {
		t.Errorf("CASE B SerpAPI calls = %d, want 0", counter.Get(ProviderSerpapi))
	}
	if counter.Get(ProviderGoogleWeb) != 0 {
		t.Errorf("CASE B Google calls = %d, want 0", counter.Get(ProviderGoogleWeb))
	}
}

func TestWebRouter_CaseC_BraveTavilyInsufficient_SerpapiSufficient(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.Write([]byte(braveResultsJSON(2))) // insufficient (<3)
	}))
	t.Cleanup(braveServer.Close)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderTavily)
		w.Write([]byte(tavilyResultsJSON(2))) // insufficient (<3)
	}))
	t.Cleanup(tavilyServer.Close)
	serpapiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderSerpapi)
		w.Write([]byte(serpapiResultsJSON(3))) // sufficient
	}))
	t.Cleanup(serpapiServer.Close)
	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderGoogleWeb)
		w.Write([]byte(googleResultsJSON(3)))
	}))
	t.Cleanup(googleServer.Close)

	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k", NewClientHTTPDoer(braveServer.Client())))
	reg.Register(ProviderTavily, NewTavilyProvider(webCfg(tavilyServer.URL), "k", NewClientHTTPDoer(tavilyServer.Client())))
	reg.Register(ProviderSerpapi, NewSerpapiProvider(webCfg(serpapiServer.URL), "k", NewClientHTTPDoer(serpapiServer.Client())))
	reg.Register(ProviderGoogleWeb, NewGoogleWebProvider(webCfgGoogle(googleServer.URL), "k", NewClientHTTPDoer(googleServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       NewMemoryCache(),
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	_, err := router.Search(context.Background(), SearchRequest{
		Query:      "case c",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if counter.Get(ProviderGoogleWeb) != 0 {
		t.Errorf("CASE C Google calls = %d, want 0", counter.Get(ProviderGoogleWeb))
	}
	if counter.Get(ProviderSerpapi) != 1 {
		t.Errorf("CASE C SerpAPI calls = %d, want 1", counter.Get(ProviderSerpapi))
	}
}

func TestWebRouter_CaseD_AllInsufficient_GoogleSufficient(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(braveServer.Close)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderTavily)
		w.Write([]byte(tavilyResultsJSON(1))) // insufficient
	}))
	t.Cleanup(tavilyServer.Close)
	serpapiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderSerpapi)
		w.Write([]byte(serpapiResultsJSON(1))) // insufficient
	}))
	t.Cleanup(serpapiServer.Close)
	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderGoogleWeb)
		w.Write([]byte(googleResultsJSON(3))) // sufficient
	}))
	t.Cleanup(googleServer.Close)

	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k", NewClientHTTPDoer(braveServer.Client())))
	reg.Register(ProviderTavily, NewTavilyProvider(webCfg(tavilyServer.URL), "k", NewClientHTTPDoer(tavilyServer.Client())))
	reg.Register(ProviderSerpapi, NewSerpapiProvider(webCfg(serpapiServer.URL), "k", NewClientHTTPDoer(serpapiServer.Client())))
	reg.Register(ProviderGoogleWeb, NewGoogleWebProvider(webCfgGoogle(googleServer.URL), "k", NewClientHTTPDoer(googleServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       NewMemoryCache(),
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	_, err := router.Search(context.Background(), SearchRequest{
		Query:      "case d",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if counter.Get(ProviderGoogleWeb) != 1 {
		t.Errorf("CASE D Google calls = %d, want 1", counter.Get(ProviderGoogleWeb))
	}
}

func TestWebRouter_CaseE_CacheAvoidsSecondExternalCall(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.Write([]byte(braveResultsJSON(3)))
	}))
	t.Cleanup(braveServer.Close)

	cache := NewMemoryCache()
	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k", NewClientHTTPDoer(braveServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       cache,
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	req := SearchRequest{Query: "case e", Intent: IntentWebGeneral, RoleID: string(RoleCEO), MaxResults: 10}

	resp1, err := router.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	if resp1.CacheHit {
		t.Error("first search must not be a cache hit")
	}
	if counter.Get(ProviderBrave) != 1 {
		t.Errorf("first search Brave calls = %d, want 1", counter.Get(ProviderBrave))
	}

	resp2, err := router.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if !resp2.CacheHit {
		t.Error("second identical request must be a cache hit")
	}
	if counter.Get(ProviderBrave) != 1 {
		t.Errorf("CASE E external calls after cache hit = %d, want 1 total", counter.Get(ProviderBrave))
	}
}

func TestWebRouter_CaseF_Brave401_FallsBackToTavily(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(braveServer.Close)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderTavily)
		w.Write([]byte(tavilyResultsJSON(3)))
	}))
	t.Cleanup(tavilyServer.Close)

	ledger := NewMemoryUsageLedger()
	logger := &fakeLogger{}

	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k", NewClientHTTPDoer(braveServer.Client())))
	reg.Register(ProviderTavily, NewTavilyProvider(webCfg(tavilyServer.URL), "k", NewClientHTTPDoer(tavilyServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       NewMemoryCache(),
		Usage:       ledger,
		Logger:      logger,
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	_, err := router.Search(context.Background(), SearchRequest{
		Query:      "case f",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if counter.Get(ProviderBrave) != 1 {
		t.Errorf("CASE F Brave calls = %d, want 1 (no useless retry on 401)", counter.Get(ProviderBrave))
	}
	if counter.Get(ProviderTavily) != 1 {
		t.Errorf("CASE F Tavily calls = %d, want 1", counter.Get(ProviderTavily))
	}

	entries, _ := ledger.Get(UsageLedgerOpts{})
	foundUnauthorized := false
	for _, e := range entries {
		if e.Provider == ProviderBrave && !e.Success && strings.Contains(strings.ToLower(e.ErrorCode), "unauthorized") {
			foundUnauthorized = true
		}
	}
	if !foundUnauthorized {
		t.Error("CASE F usage must record Brave as failed with unauthorized")
	}

	failedEvent := false
	for _, ev := range logger.GetEvents() {
		if ev.Kind == EventProviderFailed && ev.Provider == string(ProviderBrave) {
			failedEvent = true
		}
	}
	if !failedEvent {
		t.Error("CASE F observability must log provider_failed for Brave")
	}
}

func TestWebRouter_CaseG_Brave429_RateLimited_FallsBackToTavily(t *testing.T) {
	counter := &webCallCounter{}

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderBrave)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(braveServer.Close)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Hit(ProviderTavily)
		w.Write([]byte(tavilyResultsJSON(3)))
	}))
	t.Cleanup(tavilyServer.Close)

	ledger := NewMemoryUsageLedger()

	reg := NewRegistry()
	reg.Register(ProviderBrave, NewBraveProvider(webCfg(braveServer.URL), "k", NewClientHTTPDoer(braveServer.Client())))
	reg.Register(ProviderTavily, NewTavilyProvider(webCfg(tavilyServer.URL), "k", NewClientHTTPDoer(tavilyServer.Client())))

	router, _ := NewRouter(RouterConfig{
		Cache:       NewMemoryCache(),
		Usage:       ledger,
		Sufficiency: NewDefaultSufficiencyEvaluator(),
	})
	router.registry = reg

	_, err := router.Search(context.Background(), SearchRequest{
		Query:      "case g",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if counter.Get(ProviderBrave) != 1 {
		t.Errorf("CASE G Brave calls = %d, want 1 (long Retry-After must not sleep/retry)", counter.Get(ProviderBrave))
	}
	if counter.Get(ProviderTavily) != 1 {
		t.Errorf("CASE G Tavily calls = %d, want 1", counter.Get(ProviderTavily))
	}

	entries, _ := ledger.Get(UsageLedgerOpts{})
	foundRateLimited := false
	for _, e := range entries {
		if e.Provider == ProviderBrave && !e.Success && strings.Contains(strings.ToLower(e.ErrorCode), "rate_limited") {
			foundRateLimited = true
		}
	}
	if !foundRateLimited {
		t.Error("CASE G usage must record Brave as rate_limited")
	}
}
