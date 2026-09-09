package search

import (
	"context"
	"sync/atomic"
)

// fakeProvider is a test helper that implements Provider
type fakeProvider struct {
	name      string
	id        ProviderID
	supports  []SearchIntent
	callCount int32
	results   []SearchResult
	err       error
}

func (p *fakeProvider) Name() string           { return p.name }
func (p *fakeProvider) ProviderID() ProviderID { return p.id }
func (p *fakeProvider) Supports(intent SearchIntent) bool {
	for _, s := range p.supports {
		if s == intent {
			return true
		}
	}
	return false
}
func (p *fakeProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	atomic.AddInt32(&p.callCount, 1)
	return p.results, p.err
}
func (p *fakeProvider) GetCallCount() int32 { return atomic.LoadInt32(&p.callCount) }

// fakeProviderWithError is a test helper for error scenarios
type fakeProviderWithError struct {
	name     string
	id       ProviderID
	supports []SearchIntent
	err      error
}

func (f *fakeProviderWithError) Name() string           { return f.name }
func (f *fakeProviderWithError) ProviderID() ProviderID { return f.id }
func (f *fakeProviderWithError) Supports(intent SearchIntent) bool {
	for _, s := range f.supports {
		if s == intent {
			return true
		}
	}
	return false
}
func (f *fakeProviderWithError) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	return nil, f.err
}

// countingProvider tracks call count for negative invocation tests
type countingProvider struct {
	name      string
	id        ProviderID
	supports  []SearchIntent
	results   []SearchResult
	err       error
	callCount int32
}

func (p *countingProvider) Name() string           { return p.name }
func (p *countingProvider) ProviderID() ProviderID { return p.id }
func (p *countingProvider) Supports(intent SearchIntent) bool {
	for _, s := range p.supports {
		if s == intent {
			return true
		}
	}
	return false
}
func (p *countingProvider) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	atomic.AddInt32(&p.callCount, 1)
	return p.results, p.err
}
func (p *countingProvider) Calls() int32 { return atomic.LoadInt32(&p.callCount) }

// Ensure fake types implement Provider interface
var _ Provider = (*fakeProvider)(nil)
var _ Provider = (*fakeProviderWithError)(nil)
var _ Provider = (*countingProvider)(nil)
