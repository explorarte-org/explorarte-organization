package search

import (
	"context"
	"errors"
	"testing"
)

// Test that when Brave succeeds sufficiently, Tavily/SerpAPI/Google are NOT called
func TestFallback_WebGeneral_BraveSufficient_TavilyNotCalled(t *testing.T) {
	cache := NewMemoryCache()
	brave := &countingProvider{
		name:     "brave",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R1", URL: "http://e.com/1"},
			{Title: "R2", URL: "http://e.com/2"},
			{Title: "R3", URL: "http://e.com/3"},
		},
	}
	tavily := &countingProvider{
		name:     "tavily",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R4", URL: "http://e.com/4"},
		},
	}
	serpapi := &countingProvider{
		name:     "serpapi",
		id:       ProviderSerpapi,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R5", URL: "http://e.com/5"},
		},
	}
	google := &countingProvider{
		name:     "google",
		id:       ProviderGoogleWeb,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R6", URL: "http://e.com/6"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, brave)
	registry.Register(ProviderTavily, tavily)
	registry.Register(ProviderSerpapi, serpapi)
	registry.Register(ProviderGoogleWeb, google)

	router, _ := NewRouter(RouterConfig{
		Cache:       cache,
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:      "test",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	}

	_, _ = router.Search(context.Background(), req)

	// CRITICAL: Brave was sufficient, so others should NOT be called
	if brave.Calls() == 0 {
		t.Error("Brave should have been called")
	}
	if tavily.Calls() != 0 {
		t.Errorf("Tavily should NOT be called when Brave is sufficient, got calls=%d", tavily.Calls())
	}
	if serpapi.Calls() != 0 {
		t.Errorf("SerpAPI should NOT be called when Brave is sufficient, got calls=%d", serpapi.Calls())
	}
	if google.Calls() != 0 {
		t.Errorf("Google should NOT be called when Brave is sufficient, got calls=%d", google.Calls())
	}
}

// Test that BookGeneral: GoogleBooks -> OpenLibrary (no OpenAlex)
func TestFallback_BookGeneral_GoogleBooksSufficient_OpenLibraryNotCalled(t *testing.T) {
	googlebooks := &countingProvider{
		name:     "googlebooks",
		id:       ProviderGoogleBooks,
		supports: []SearchIntent{IntentBookGeneral},
		results: []SearchResult{
			{Title: "Book 1", URL: "http://books.google.com/1"},
			{Title: "Book 2", URL: "http://books.google.com/2"},
			{Title: "Book 3", URL: "http://books.google.com/3"},
		},
	}
	openlibrary := &countingProvider{
		name:     "openlibrary",
		id:       ProviderOpenlibrary,
		supports: []SearchIntent{IntentBookGeneral},
		results: []SearchResult{
			{Title: "Book 4", URL: "http://openlibrary.org/4"},
		},
	}
	// OpenAlex should NOT be in book_general route
	openalex := &countingProvider{
		name:     "openalex",
		id:       ProviderOpenAlex,
		supports: []SearchIntent{IntentBookGeneral},
		results: []SearchResult{
			{Title: "Paper", URL: "http://openalex.org/paper"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderGoogleBooks, googlebooks)
	registry.Register(ProviderOpenlibrary, openlibrary)
	registry.Register(ProviderOpenAlex, openalex)

	router, _ := NewRouter(RouterConfig{
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "books",
		Intent: IntentBookGeneral,
		RoleID: string(RoleResearch),
	}

	_, _ = router.Search(context.Background(), req)

	if googlebooks.Calls() == 0 {
		t.Error("GoogleBooks should have been called")
	}
	if openlibrary.Calls() != 0 {
		t.Errorf("OpenLibrary should NOT be called when GoogleBooks is sufficient, got calls=%d", openlibrary.Calls())
	}
	if openalex.Calls() != 0 {
		t.Errorf("OpenAlex should NOT be in book_general route at all, got calls=%d", openalex.Calls())
	}
}

// Test BookAcademicOA: DOAB -> OAPEN (no GoogleBooks)
func TestFallback_BookAcademicOA_DOABSufficient_OAPENNotCalled(t *testing.T) {
	doab := &countingProvider{
		name:     "doab",
		id:       ProviderDoab,
		supports: []SearchIntent{IntentBookAcademicOA},
		results: []SearchResult{
			{Title: "OA Book 1", URL: "http://doab.org/1"},
			{Title: "OA Book 2", URL: "http://doab.org/2"},
			{Title: "OA Book 3", URL: "http://doab.org/3"},
		},
	}
	oapen := &countingProvider{
		name:     "oapen",
		id:       ProviderOapen,
		supports: []SearchIntent{IntentBookAcademicOA},
		results: []SearchResult{
			{Title: "OA Book 4", URL: "http://oapen.org/4"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderDoab, doab)
	registry.Register(ProviderOapen, oapen)

	router, _ := NewRouter(RouterConfig{
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "open access academic books",
		Intent: IntentBookAcademicOA,
		RoleID: string(RoleResearch),
	}

	_, _ = router.Search(context.Background(), req)

	if doab.Calls() == 0 {
		t.Error("DOAB should have been called")
	}
	if oapen.Calls() != 0 {
		t.Errorf("OAPEN should NOT be called when DOAB is sufficient, got calls=%d", oapen.Calls())
	}
}

// Test BookPublicDomain: Gutendex only (no OpenLibrary unless Gutendex fails)
func TestFallback_BookPublicDomain_GutendexSufficient_OnlyGutendexCalled(t *testing.T) {
	gutendex := &countingProvider{
		name:     "gutendex",
		id:       ProviderGutendex,
		supports: []SearchIntent{IntentBookPublicDomain},
		results: []SearchResult{
			{Title: "Gutenberg Book 1", URL: "http://gutendex.com/1"},
			{Title: "Gutenberg Book 2", URL: "http://gutendex.com/2"},
			{Title: "Gutenberg Book 3", URL: "http://gutendex.com/3"},
		},
	}
	openlibrary := &countingProvider{
		name:     "openlibrary",
		id:       ProviderOpenlibrary,
		supports: []SearchIntent{IntentBookPublicDomain},
		results: []SearchResult{
			{Title: "OL Book", URL: "http://openlibrary.org/book"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderGutendex, gutendex)
	registry.Register(ProviderOpenlibrary, openlibrary)

	router, _ := NewRouter(RouterConfig{
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "public domain books",
		Intent: IntentBookPublicDomain,
		RoleID: string(RoleResearch),
	}

	_, _ = router.Search(context.Background(), req)

	if gutendex.Calls() == 0 {
		t.Error("Gutendex should have been called")
	}
	if openlibrary.Calls() != 0 {
		t.Errorf("OpenLibrary should NOT be called when Gutendex is sufficient, got calls=%d", openlibrary.Calls())
	}
}

// Test fallback chain: Brave fails -> Tavily called -> SerpAPI called -> Google called
func TestFallback_WebGeneral_Chain_BraveFails_TavilyCalled_NotSerpAPIOrGoogle(t *testing.T) {
	brave := &countingProvider{
		name:     "brave",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		err:      errors.New("brave error"),
	}
	tavily := &countingProvider{
		name:     "tavily",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R1", URL: "http://e.com/1"},
			{Title: "R2", URL: "http://e.com/2"},
			{Title: "R3", URL: "http://e.com/3"},
		},
	}
	serpapi := &countingProvider{
		name:     "serpapi",
		id:       ProviderSerpapi,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R4", URL: "http://e.com/4"},
		},
	}
	google := &countingProvider{
		name:     "google",
		id:       ProviderGoogleWeb,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R5", URL: "http://e.com/5"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, brave)
	registry.Register(ProviderTavily, tavily)
	registry.Register(ProviderSerpapi, serpapi)
	registry.Register(ProviderGoogleWeb, google)

	router, _ := NewRouter(RouterConfig{
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:      "test",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	}

	_, _ = router.Search(context.Background(), req)

	// Brave failed, Tavily was sufficient (3 results)
	if brave.Calls() != 1 {
		t.Errorf("Brave should be called once (and fail), got %d", brave.Calls())
	}
	if tavily.Calls() != 1 {
		t.Errorf("Tavily should be called once (and succeed), got %d", tavily.Calls())
	}
	if serpapi.Calls() != 0 {
		t.Errorf("SerpAPI should NOT be called when Tavily is sufficient, got calls=%d", serpapi.Calls())
	}
	if google.Calls() != 0 {
		t.Errorf("Google should NOT be called when Tavily is sufficient, got calls=%d", google.Calls())
	}
}

// Test insufficient results triggers fallback
func TestFallback_Insufficient_TriggersNextProvider(t *testing.T) {
	provider1 := &countingProvider{
		name:     "p1",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R1", URL: "http://e.com/1"}, // only 1 result - insufficient
		},
	}
	provider2 := &countingProvider{
		name:     "p2",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R2", URL: "http://e.com/2"},
			{Title: "R3", URL: "http://e.com/3"},
			{Title: "R4", URL: "http://e.com/4"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider1)
	registry.Register(ProviderTavily, provider2)

	router, _ := NewRouter(RouterConfig{
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	// provider1 was insufficient, provider2 should be called
	if provider1.Calls() != 1 {
		t.Errorf("provider1 should be called once, got %d", provider1.Calls())
	}
	if provider2.Calls() != 1 {
		t.Errorf("provider2 should be called (fallback from insufficient), got %d", provider2.Calls())
	}
}

// Test error triggers fallback
func TestFallback_Error_TriggersNextProvider(t *testing.T) {
	provider1 := &countingProvider{
		name:     "p1",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		err:      errors.New("error"),
	}
	provider2 := &countingProvider{
		name:     "p2",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R1", URL: "http://e.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider1)
	registry.Register(ProviderTavily, provider2)

	router, _ := NewRouter(RouterConfig{})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	if provider1.Calls() != 1 {
		t.Errorf("provider1 should be called once (and fail), got %d", provider1.Calls())
	}
	if provider2.Calls() != 1 {
		t.Errorf("provider2 should be called (fallback from error), got %d", provider2.Calls())
	}
}

// Test empty results triggers fallback
func TestFallback_Empty_TriggersNextProvider(t *testing.T) {
	provider1 := &countingProvider{
		name:     "p1",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results:  []SearchResult{},
	}
	provider2 := &countingProvider{
		name:     "p2",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "R1", URL: "http://e.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider1)
	registry.Register(ProviderTavily, provider2)

	router, _ := NewRouter(RouterConfig{})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	if provider1.Calls() != 1 {
		t.Errorf("provider1 should be called once (empty), got %d", provider1.Calls())
	}
	if provider2.Calls() != 1 {
		t.Errorf("provider2 should be called (fallback from empty), got %d", provider2.Calls())
	}
}
