package search

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeLogger struct {
	mu     sync.Mutex
	events []Event
}

func (f *fakeLogger) Log(e Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeLogger) GetEvents() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Event, len(f.events))
	copy(out, f.events)
	return out
}

func TestObservability_ProviderStarted_Logged(t *testing.T) {
	logger := &fakeLogger{}

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
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	events := logger.GetEvents()

	found := false
	for _, e := range events {
		if e.Kind == EventProviderStarted {
			found = true
			if e.Provider != string(ProviderBrave) {
				t.Errorf("expected provider=brave_web, got %s", e.Provider)
			}
		}
	}
	if !found {
		t.Error("expected EventProviderStarted to be logged")
	}
}

func TestObservability_ProviderCompleted_Logged(t *testing.T) {
	logger := &fakeLogger{}

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
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	events := logger.GetEvents()

	found := false
	for _, e := range events {
		if e.Kind == EventProviderCompleted {
			found = true
		}
	}
	if !found {
		t.Error("expected EventProviderCompleted to be logged")
	}
}

func TestObservability_ProviderFailed_Logged(t *testing.T) {
	logger := &fakeLogger{}

	provider := &fakeProviderWithError{
		name:     "failing",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		err:      errors.New("provider error"),
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Logger: logger,
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	events := logger.GetEvents()

	found := false
	for _, e := range events {
		if e.Kind == EventProviderFailed {
			found = true
			if e.Error == "" {
				t.Error("expected error message in failed event")
			}
		}
	}
	if !found {
		t.Error("expected EventProviderFailed to be logged")
	}
}

func TestObservability_Completed_Logged(t *testing.T) {
	logger := &fakeLogger{}

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
		Logger:      logger,
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	events := logger.GetEvents()

	found := false
	for _, e := range events {
		if e.Kind == EventCompleted {
			found = true
			if e.Results < 3 {
				t.Errorf("expected at least 3 results in completed event, got %d", e.Results)
			}
		}
	}
	if !found {
		t.Error("expected EventCompleted to be logged")
	}
}

func TestObservability_Insufficient_Logged(t *testing.T) {
	logger := &fakeLogger{}

	provider := &fakeProvider{
		name:     "test",
		id:       ProviderBrave,
		supports: []SearchIntent{IntentWebGeneral},
		results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1"},
			{Title: "Result 2", URL: "http://example.com/2"},
		},
	}

	registry := NewRegistry()
	registry.Register(ProviderBrave, provider)

	router, _ := NewRouter(RouterConfig{
		Logger:      logger,
		Sufficiency: &DefaultSufficiencyEvaluator{},
	})
	router.registry = registry

	req := SearchRequest{
		Query:  "test",
		Intent: IntentWebGeneral,
		RoleID: string(RoleCEO),
	}

	_, _ = router.Search(context.Background(), req)

	events := logger.GetEvents()

	found := false
	for _, e := range events {
		if e.Kind == EventProviderInsufficient {
			found = true
		}
	}
	if !found {
		t.Error("expected EventProviderInsufficient to be logged")
	}
}
