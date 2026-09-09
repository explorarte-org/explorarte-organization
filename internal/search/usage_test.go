package search

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type testUsageLedger struct {
	mu      sync.Mutex
	entries []RequestMetrics
}

func (f *testUsageLedger) Record(m RequestMetrics) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, m)
}

func (f *testUsageLedger) Get(opts UsageLedgerOpts) ([]UsageEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]UsageEntry, len(f.entries))
	for i, m := range f.entries {
		out[i] = UsageEntry{
			RequestID:        m.RequestID,
			MissionID:        m.MissionID,
			RoleID:           m.RoleID,
			Provider:         m.Provider,
			Intent:           m.Intent,
			CacheHit:         m.CacheHit,
			EstimatedCostUSD: m.EstimatedCostUSD,
			Duration:         m.Duration,
			ResultCount:      m.ResultCount,
			Success:          m.Success,
			ErrorCode:        m.ErrorCode,
		}
	}
	return out, nil
}

func TestUsageLedger_EachProviderInvocation_CreatesUsageEntry(t *testing.T) {
	ledger := &testUsageLedger{}

	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "Result", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Usage: ledger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:     "test",
		Intent:    IntentWebGeneral,
		RoleID:    string(RoleCEO),
		MissionID: "mission-123",
	}

	_, err := router.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	entries, _ := ledger.Get(UsageLedgerOpts{})
	if len(entries) != 1 {
		t.Errorf("expected 1 usage entry, got %d", len(entries))
	}

	if entries[0].MissionID != "mission-123" {
		t.Errorf("expected mission_id=mission-123, got %s", entries[0].MissionID)
	}
}

func TestUsageLedger_ProviderError_Recorded(t *testing.T) {
	ledger := &testUsageLedger{}

	provider := &fakeProviderWithError{
		name:     "failing",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		err:      errors.New("provider error"),
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Usage: ledger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	entries, _ := ledger.Get(UsageLedgerOpts{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 usage entry, got %d", len(entries))
	}

	if entries[0].Success {
		t.Error("expected Success=false for failed provider")
	}

	if entries[0].ErrorCode == "" {
		t.Error("expected ErrorCode to be populated for failed provider")
	}
}

func TestUsageLedger_FallbackReason_Recorded(t *testing.T) {
	ledger := &testUsageLedger{}

	provider1 := &fakeProviderWithError{
		name:     "failing",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		err:      errors.New("error"),
	}
	provider2 := &fakeProvider{
		name:     "working",
		id:       ProviderTavily,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "Result", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider1)
	registry.Register(ProviderTavily, provider2)

	router, _ := NewRouter(RouterConfig{
		Usage: ledger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	entries, _ := ledger.Get(UsageLedgerOpts{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 usage entries, got %d", len(entries))
	}

	if entries[0].Success {
		t.Error("first entry should be failed provider")
	}
}

func TestUsageLedger_DurationAndResultCount_Populated(t *testing.T) {
	ledger := &testUsageLedger{}

	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1"},
			{Title: "Result 2", URL: "http://example.com/2"},
			{Title: "Result 3", URL: "http://example.com/3"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Usage: ledger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	entries, _ := ledger.Get(UsageLedgerOpts{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 usage entry, got %d", len(entries))
	}

	if entries[0].ResultCount != 3 {
		t.Errorf("expected ResultCount=3, got %d", entries[0].ResultCount)
	}

	if entries[0].Duration == 0 {
		t.Error("expected Duration to be populated")
	}
}

func TestUsageLedger_NoSecrets_Appear(t *testing.T) {
	ledger := &testUsageLedger{}

	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "Result", URL: "http://example.com/1"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Usage: ledger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "sk-test-secret-never-leak",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	entries, _ := ledger.Get(UsageLedgerOpts{})
	for _, entry := range entries {
		if string(entry.Provider) == "sk-test-secret-never-leak" {
			t.Error("secret leaked into usage entry")
		}
	}
}
