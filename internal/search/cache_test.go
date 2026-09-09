package search

import (
	"context"
	"testing"
	"time"
)

func TestCache_FirstRequest_CallsProvider(t *testing.T) {
	cache := NewMemoryCache()
	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Provider: "test", Title: "Result 1", URL: "http://example.com/1"},
			{Provider: "test", Title: "Result 2", URL: "http://example.com/2"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Cache: cache,
	})
	router.registry = registry

	req := SearchRequest{
		Query:      "test query",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	}

	ctx := context.Background()
	_, err := router.Search(ctx, req)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if provider.GetCallCount() != 1 {
		t.Errorf("expected provider to be called once, got %d", provider.GetCallCount())
	}
}

func TestCache_SecondIdenticalRequest_UsesCache(t *testing.T) {
	cache := NewMemoryCache()
	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Provider: "test", Title: "Result 1", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Cache: cache,
	})
	router.registry = registry

	req := SearchRequest{
		Query:      "test query",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	}

	ctx := context.Background()

	// First request
	_, err := router.Search(ctx, req)
	if err != nil {
		t.Fatalf("first search failed: %v", err)
	}

	// Second identical request
	_, err = router.Search(ctx, req)
	if err != nil {
		t.Fatalf("second search failed: %v", err)
	}

	// Provider should only be called once
	if provider.GetCallCount() != 1 {
		t.Errorf("expected provider called once (second request should use cache), got %d", provider.GetCallCount())
	}
}

func TestCache_DifferentQuery_DoesNotUseCache(t *testing.T) {
	cache := NewMemoryCache()
	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Provider: "test", Title: "Result", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Cache: cache,
	})
	router.registry = registry

	ctx := context.Background()

	// First request
	req1 := SearchRequest{Query: "query1", Intent: IntentWebGeneral, RoleID: string(RoleCEO)}
	_, _ = router.Search(ctx, req1)

	// Different query
	req2 := SearchRequest{Query: "query2", Intent: IntentWebGeneral, RoleID: string(RoleCEO)}
	_, _ = router.Search(ctx, req2)

	if provider.GetCallCount() != 2 {
		t.Errorf("expected provider called twice for different queries, got %d", provider.GetCallCount())
	}
}

func TestCache_DifferentRole_UsesCacheOnlyIfQueryMatches(t *testing.T) {
	cache := NewMemoryCache()
	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Provider: "test", Title: "Result", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Cache: cache,
	})
	router.registry = registry

	ctx := context.Background()

	// First request as CEO
	req1 := SearchRequest{Query: "test", Intent: IntentWebGeneral, RoleID: string(RoleCEO)}
	_, _ = router.Search(ctx, req1)

	// Same query as Marketing (different role)
	req2 := SearchRequest{Query: "test", Intent: IntentWebGeneral, RoleID: string(RoleMarketing)}
	_, _ = router.Search(ctx, req2)

	// Provider should be called twice because cache key includes role
	if provider.GetCallCount() != 2 {
		t.Errorf("expected provider called twice (different roles), got %d", provider.GetCallCount())
	}
}

func TestCache_ExpiredTTL_ExecutesProviderAgain(t *testing.T) {
	cache := NewMemoryCache()
	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Provider: "test", Title: "Result", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Cache: cache,
	})
	router.registry = registry

	// Override cache TTL to be very short for testing
	originalTTL := DefaultTTLs[IntentWebGeneral]
	DefaultTTLs[IntentWebGeneral] = 1 * time.Millisecond

	ctx := context.Background()

	req := SearchRequest{
		Query:      "test",
		Intent:     IntentWebGeneral,
		RoleID:     string(RoleCEO),
		MaxResults: 10,
	}

	// First request
	_, _ = router.Search(ctx, req)

	// Wait for TTL to expire
	time.Sleep(10 * time.Millisecond)

	// Second request after TTL expired
	_, _ = router.Search(ctx, req)

	// Restore original TTL
	DefaultTTLs[IntentWebGeneral] = originalTTL

	if provider.GetCallCount() != 2 {
		t.Errorf("expected provider called twice after TTL expired, got %d", provider.GetCallCount())
	}
}

func TestCache_CacheKeyIncludesIntentAndQueryAndRole(t *testing.T) {
	cache := NewMemoryCache()
	brave := &fakeProvider{
		name:     "brave",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Provider: "brave", Title: "Result", URL: "http://example.com/1"},
		},
	}
	tavily := &fakeProvider{
		name:     "tavily",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentNews},
		results: []SearchResult{
			{Provider: "tavily", Title: "Result", URL: "http://example.com/2"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, brave)
	registry.Register(ProviderTavily, tavily)

	router, _ := NewRouter(RouterConfig{
		Cache: cache,
	})
	router.registry = registry

	ctx := context.Background()

	// Request web_general - should call Brave
	req1 := SearchRequest{Query: "test", Intent: IntentWebGeneral, RoleID: string(RoleCEO)}
	_, _ = router.Search(ctx, req1)

	// Same query but news intent - should call Tavily (not use cache from web_general)
	req2 := SearchRequest{Query: "test", Intent: IntentNews, RoleID: string(RoleCEO)}
	_, _ = router.Search(ctx, req2)

	// Both providers should be called because intents are different
	if brave.GetCallCount() != 1 {
		t.Errorf("expected Brave called once, got %d", brave.GetCallCount())
	}
	if tavily.GetCallCount() != 1 {
		t.Errorf("expected Tavily called once (different intent), got %d", tavily.GetCallCount())
	}
}
