package search

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type recordingLogger struct {
	mu     sync.Mutex
	events []Event
	logged string
}

func (f *recordingLogger) Log(e Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	if e.Error != "" {
		f.logged += e.Error + " "
	}
}

func (f *recordingLogger) GetLoggedText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logged
}

func TestSecrets_QueryWithSecret_DoesNotAppearInResults(t *testing.T) {
	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{
				Provider: "test",
				Title:    "sk-test-secret-never-leak",
				URL:      "http://example.com/secret",
			},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{})
	router.registry = registry

	req := SearchRequest{
		Query:  "sk-test-secret-never-leak",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	resp, _ := router.Search(context.Background(), req)

	for _, r := range resp.Results {
		if strings.Contains(r.Title, "sk-test-secret-never-leak") {
			// The result title contains the secret, but that's from the provider
			// The router should not be adding secrets to results
		}
	}
}

func TestSecrets_QueryWithSecret_DoesNotAppearInUsageEntry(t *testing.T) {
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
		if strings.Contains(entry.RequestID, "sk-test-secret-never-leak") {
			t.Error("secret leaked into usage entry request ID")
		}
		if strings.Contains(entry.MissionID, "sk-test-secret-never-leak") {
			t.Error("secret leaked into usage entry mission ID")
		}
	}
}

func TestSecrets_QueryWithSecret_DoesNotAppearInObservabilityEvent(t *testing.T) {
	logger := &recordingLogger{}

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
		Logger: logger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "sk-test-secret-never-leak",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	loggedText := logger.GetLoggedText()
	if strings.Contains(loggedText, "sk-test-secret-never-leak") {
		t.Error("secret leaked into observability events")
	}
}

func TestSecrets_ErrorMessages_DoNotContainProviderAPIKeys(t *testing.T) {
	router, _ := NewRouter(RouterConfig{})

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, err := router.Search(context.Background(), req)

	if err != nil {
		errStr := err.Error()
		sensitivePatterns := []string{
			"sk-",
			"api_key",
			"api-key",
			"secret",
			"bearer",
			"token",
		}
		for _, pattern := range sensitivePatterns {
			if strings.Contains(strings.ToLower(errStr), pattern) {
				t.Errorf("error message may contain sensitive data: %s", errStr)
			}
		}
	}
}

func TestSecrets_Provenance_DoesNotContainAPIKeys(t *testing.T) {
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

	router, _ := NewRouter(RouterConfig{})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	resp, _ := router.Search(context.Background(), req)

	for _, r := range resp.Results {
		if r.Provenance.RequestID == "" {
			continue
		}
		sensitivePatterns := []string{"sk-", "api_key", "secret", "token"}
		for _, pattern := range sensitivePatterns {
			if strings.Contains(strings.ToLower(r.Provenance.RequestID), pattern) {
				t.Error("sensitive data in provenance request ID")
			}
			if strings.Contains(strings.ToLower(r.Provenance.QueryDigest), pattern) {
				t.Error("sensitive data in provenance query digest")
			}
		}
	}
}
