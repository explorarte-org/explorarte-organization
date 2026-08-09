package rag

import (
	"context"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

type fakePricingStore struct{ tier modelpricing.PriceTier }

func (s *fakePricingStore) ListTiers(context.Context, string, string, modelpricing.BillingMode, time.Time) ([]modelpricing.PriceTier, error) {
	return []modelpricing.PriceTier{s.tier}, nil
}
func (s *fakePricingStore) Upsert(_ context.Context, tier modelpricing.PriceTier) (modelpricing.PriceTier, error) {
	return tier, nil
}

func newTestPricingService(t *testing.T) *modelpricing.Service {
	t.Helper()
	service, err := modelpricing.NewService(&fakePricingStore{tier: modelpricing.PriceTier{
		ProviderID: "gemini", ProviderModelID: "gemini-embedding-2", ContextTierName: "default",
		InputPriceNanosPerMillion: 200_000_000, BillingMode: modelpricing.BillingOnline, EffectiveAt: time.Now().Add(-time.Hour),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type fakeEmbeddingLedger struct {
	balanceOK          bool
	createErr          error
	reserveErr         error
	reconcileErr       error
	releaseCalled      bool
	reconcileCalled    bool
	createdInvocations int
}

func (l *fakeEmbeddingLedger) GetWallet(context.Context, string) (costledger.ProviderWallet, error) {
	return costledger.ProviderWallet{}, nil
}
func (l *fakeEmbeddingLedger) SetBalance(context.Context, string, modelpricing.USDNanos, time.Time) (costledger.ProviderWallet, error) {
	return costledger.ProviderWallet{}, nil
}
func (l *fakeEmbeddingLedger) Reserve(context.Context, string, int64, modelpricing.USDNanos, time.Time) error {
	return nil
}
func (l *fakeEmbeddingLedger) Reconcile(context.Context, string, int64, modelpricing.USDNanos, time.Time) error {
	return nil
}
func (l *fakeEmbeddingLedger) Release(context.Context, string, int64, time.Time) error { return nil }
func (l *fakeEmbeddingLedger) ListEvents(context.Context, string, int) ([]costledger.WalletEvent, error) {
	return nil, nil
}
func (l *fakeEmbeddingLedger) ListOrphanedReservations(context.Context, time.Time, int) ([]costledger.WalletEvent, error) {
	return nil, nil
}
func (l *fakeEmbeddingLedger) CreateEmbeddingInvocation(_ context.Context, invocation costledger.EmbeddingInvocation) (costledger.EmbeddingInvocation, error) {
	if l.createErr != nil {
		return costledger.EmbeddingInvocation{}, l.createErr
	}
	l.createdInvocations++
	invocation.ID = int64(l.createdInvocations)
	return invocation, nil
}
func (l *fakeEmbeddingLedger) ReserveEmbedding(context.Context, string, int64, modelpricing.USDNanos, time.Time) error {
	if l.reserveErr != nil {
		return l.reserveErr
	}
	if !l.balanceOK {
		return costledger.ErrInsufficientBalance
	}
	return nil
}
func (l *fakeEmbeddingLedger) ReconcileEmbedding(context.Context, string, int64, modelpricing.USDNanos, time.Time) error {
	l.reconcileCalled = true
	return l.reconcileErr
}
func (l *fakeEmbeddingLedger) ReleaseEmbedding(context.Context, string, int64, time.Time) error {
	l.releaseCalled = true
	return nil
}

var _ costledger.Ledger = (*fakeEmbeddingLedger)(nil)

type fakeOnlineAdapter struct {
	vector  []float32
	err     error
	tokens  int64
	entered int
}

func (a *fakeOnlineAdapter) Embed(_ context.Context, request embeddingruntime.EmbedRequest) (embeddingruntime.EmbedResponse, error) {
	a.entered++
	if a.err != nil {
		return embeddingruntime.EmbedResponse{}, a.err
	}
	return embeddingruntime.EmbedResponse{
		Results:     []embeddingruntime.EmbedResult{{Key: request.Items[0].Key, Vector: a.vector}},
		InputTokens: a.tokens,
	}, nil
}

func testSemanticDeps(ledger *fakeEmbeddingLedger, adapter *fakeOnlineAdapter, budgets agentbudget.Ledger, t *testing.T) *SemanticSearchDeps {
	return &SemanticSearchDeps{
		OnlineAdapter: adapter, Pricing: newTestPricingService(t), Wallet: ledger, Budgets: budgets,
		ProviderID: "gemini", ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768, PromptTemplateVersion: "prompt-template.v1",
	}
}

func newSemanticQueryManager(t *testing.T, semantic *SemanticSearchDeps) (*Manager, *fakeRepository) {
	t.Helper()
	repo := newFakeRepository()
	repo.activeGeneration["own:solo"] = "gen-1"
	repo.generations["gen-1"] = IndexGeneration{ID: "gen-1", Status: GenerationActive}
	namespaces := &fakeNamespaces{own: "solo"}
	manager, err := NewManager(NewService(nil), repo, &recordingGate{}, namespaces, semantic)
	if err != nil {
		t.Fatal(err)
	}
	return manager, repo
}

func TestQueryEmbedsSuccessfullyAndReconciles(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	manager, repo := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	if _, err := manager.Query(context.Background(), QueryRequest{OrganizationID: "org", ActorRoleID: "role", Scope: NamespaceOwn, QueryText: "reactor overheating"}); err != nil {
		t.Fatal(err)
	}
	if adapter.entered != 1 {
		t.Fatalf("adapter called %d times, want 1", adapter.entered)
	}
	if !ledger.reconcileCalled {
		t.Fatal("expected ReconcileEmbedding to be called after a successful embed")
	}
	if ledger.releaseCalled {
		t.Fatal("did not expect ReleaseEmbedding on the success path")
	}
	if len(repo.lastQueryCommand.QueryVector) != 2 {
		t.Fatalf("QueryVector not passed through to repository.Query: %+v", repo.lastQueryCommand)
	}
}

func TestQuerySkipsEmbeddingWhenSemanticDepsAreNil(t *testing.T) {
	manager, repo := newSemanticQueryManager(t, nil)
	if _, err := manager.Query(context.Background(), QueryRequest{OrganizationID: "org", ActorRoleID: "role", Scope: NamespaceOwn, QueryText: "anything"}); err != nil {
		t.Fatal(err)
	}
	if repo.lastQueryCommand.QueryVector != nil {
		t.Fatalf("QueryVector=%v want nil when semantic search is not configured", repo.lastQueryCommand.QueryVector)
	}
}

func TestQuerySkipsEmbeddingWhenQueryTextIsClassifiedSecret(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1}, tokens: 5}
	manager, repo := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	// AWS-style access key pattern — matched by internal/dataclassifier as a
	// secret. A query containing one must never reach the embedding
	// provider, even though it's just a search string, not stored content.
	secretQuery := "find the incident involving AKIAABCDEFGHIJKLMNOP"
	if _, err := manager.Query(context.Background(), QueryRequest{OrganizationID: "org", ActorRoleID: "role", Scope: NamespaceOwn, QueryText: secretQuery}); err != nil {
		t.Fatal(err)
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter called %d times, want 0 — secret content must never be sent to the provider", adapter.entered)
	}
	if ledger.createdInvocations != 0 {
		t.Fatalf("createdInvocations=%d want 0 — no wallet spend for content that was never sent", ledger.createdInvocations)
	}
	if repo.lastQueryCommand.QueryVector != nil {
		t.Fatal("expected nil QueryVector when the query text is classified secret")
	}
}

func TestQuerySkipsEmbeddingOnInsufficientBalanceWithoutFailingQuery(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: false}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1}, tokens: 5}
	manager, repo := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	if _, err := manager.Query(context.Background(), QueryRequest{OrganizationID: "org", ActorRoleID: "role", Scope: NamespaceOwn, QueryText: "reactor overheating"}); err != nil {
		t.Fatalf("Query must succeed with exact+lexical results even when the vector channel can't be afforded: %v", err)
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter called %d times, want 0 — must never call the provider after a rejected reservation", adapter.entered)
	}
	if repo.lastQueryCommand.QueryVector != nil {
		t.Fatal("expected nil QueryVector on insufficient balance")
	}
}

func TestQueryReleasesReservationWhenProviderCallFails(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{err: context.DeadlineExceeded}
	manager, repo := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	if _, err := manager.Query(context.Background(), QueryRequest{OrganizationID: "org", ActorRoleID: "role", Scope: NamespaceOwn, QueryText: "reactor overheating"}); err != nil {
		t.Fatalf("Query must still succeed when the provider call fails: %v", err)
	}
	if !ledger.releaseCalled {
		t.Fatal("expected the reservation to be released after a provider failure")
	}
	if ledger.reconcileCalled {
		t.Fatal("must not reconcile a reservation for a call that never produced a real result")
	}
	if repo.lastQueryCommand.QueryVector != nil {
		t.Fatal("expected nil QueryVector after a provider failure")
	}
}
