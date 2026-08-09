package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// searchableMemoryRepository extends memoryRepository with the
// EmbeddingRepository methods Search needs — kept separate from the base
// fake so tests that don't care about search keep using the simpler one.
type searchableMemoryRepository struct {
	*memoryRepository
	embeddings   []EntryEmbedding
	searchResult []Entry
	searchErr    error
}

func newSearchableMemoryRepository() *searchableMemoryRepository {
	return &searchableMemoryRepository{memoryRepository: newMemoryRepository()}
}

func (r *searchableMemoryRepository) InsertEntryEmbedding(_ context.Context, embedding EntryEmbedding) error {
	r.embeddings = append(r.embeddings, embedding)
	return nil
}

func (r *searchableMemoryRepository) NearestEntries(context.Context, string, string, []float32, int) ([]ScoredEntry, error) {
	return nil, nil
}

func (r *searchableMemoryRepository) Search(context.Context, string, string, string, []float32, EmbeddingIdentity, string, int) ([]Entry, error) {
	return r.searchResult, r.searchErr
}

var _ EmbeddingRepository = (*searchableMemoryRepository)(nil)

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
	balanceOK       bool
	releaseCalled   bool
	reconcileCalled bool
	invocations     int
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
	l.invocations++
	invocation.ID = int64(l.invocations)
	return invocation, nil
}
func (l *fakeEmbeddingLedger) ReserveEmbedding(context.Context, string, int64, modelpricing.USDNanos, time.Time) error {
	if !l.balanceOK {
		return costledger.ErrInsufficientBalance
	}
	return nil
}
func (l *fakeEmbeddingLedger) ReconcileEmbedding(context.Context, string, int64, modelpricing.USDNanos, time.Time) error {
	l.reconcileCalled = true
	return nil
}
func (l *fakeEmbeddingLedger) ReleaseEmbedding(context.Context, string, int64, time.Time) error {
	l.releaseCalled = true
	return nil
}

var _ costledger.Ledger = (*fakeEmbeddingLedger)(nil)

type fakeOnlineAdapter struct {
	vector  []float32
	err     error
	entered int
}

func (a *fakeOnlineAdapter) Embed(_ context.Context, request embeddingruntime.EmbedRequest) (embeddingruntime.EmbedResponse, error) {
	a.entered++
	if a.err != nil {
		return embeddingruntime.EmbedResponse{}, a.err
	}
	return embeddingruntime.EmbedResponse{Results: []embeddingruntime.EmbedResult{{Key: request.Items[0].Key, Vector: a.vector}}, InputTokens: 5}, nil
}

func testSemanticDeps(t *testing.T, repo EmbeddingRepository, ledger *fakeEmbeddingLedger, adapter *fakeOnlineAdapter, budgets agentbudget.Ledger) *SemanticSearchDeps {
	t.Helper()
	return &SemanticSearchDeps{
		InsertVector: func(ctx context.Context, organizationID, entryID, inputHash string, vector []float32, createdAt time.Time) error {
			return repo.InsertEntryEmbedding(ctx, EntryEmbedding{
				OrganizationID: organizationID, EntryID: entryID, EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1",
				EmbeddingDimension: 768, PromptTemplateVersion: "prompt-template.v1", InputHash: inputHash, Vector: vector, CreatedAt: createdAt,
			})
		},
		OnlineAdapter: adapter, Pricing: newTestPricingService(t), Wallet: ledger, Budgets: budgets,
		ProviderID: "gemini", ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768, PromptTemplateVersion: "prompt-template.v1",
		Identity: EmbeddingIdentity{ModelID: "gemini-embedding-2", ModelVersion: "v1"},
	}
}

func TestReviewEmbedsOnlyOnApprove(t *testing.T) {
	clock := &fixedClock{now: time.Now().UTC()}
	repo := newSearchableMemoryRepository()
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}}
	manager, err := NewManager(NewService(clock), repo, &recordingGate{}, testSemanticDeps(t, repo, ledger, adapter, nil))
	if err != nil {
		t.Fatal(err)
	}
	command := validCommand(clock.now)
	command.ID = "mem-approve"
	entry, _, err := manager.Propose(context.Background(), ProposeRequest{Command: command, IdempotencyKey: "idem-approve"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Review(context.Background(), ReviewRequest{
		Mutation: MutationRequest{OrganizationID: entry.OrganizationID, EntryID: entry.ID, ExpectedRevision: 1, ActorRoleID: "empresa/human", Reason: "ok"},
		Outcome:  ReviewApprove,
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.entered != 1 {
		t.Fatalf("adapter entered=%d want 1 after approve", adapter.entered)
	}
	if len(repo.embeddings) != 1 {
		t.Fatalf("embeddings persisted=%d want 1 after approve", len(repo.embeddings))
	}
}

func TestReviewDoesNotEmbedOnReject(t *testing.T) {
	clock := &fixedClock{now: time.Now().UTC()}
	repo := newSearchableMemoryRepository()
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1}}
	manager, err := NewManager(NewService(clock), repo, &recordingGate{}, testSemanticDeps(t, repo, ledger, adapter, nil))
	if err != nil {
		t.Fatal(err)
	}
	command := validCommand(clock.now)
	command.ID = "mem-reject"
	entry, _, err := manager.Propose(context.Background(), ProposeRequest{Command: command, IdempotencyKey: "idem-reject"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Review(context.Background(), ReviewRequest{
		Mutation: MutationRequest{OrganizationID: entry.OrganizationID, EntryID: entry.ID, ExpectedRevision: 1, ActorRoleID: "empresa/human", Reason: "no"},
		Outcome:  ReviewReject,
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter entered=%d want 0 after reject — a rejected entry must never be embedded", adapter.entered)
	}
}

func TestSearchRejectsCrossRoleRequests(t *testing.T) {
	manager, err := NewManager(NewService(nil), newMemoryRepository(), &recordingGate{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Search(context.Background(), SearchRequest{
		OrganizationID: "org", ActorRoleID: "role-a", RoleID: "role-b", QueryText: "anything",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v want ErrInvalidRequest for a cross-role search", err)
	}
}

func TestSearchFallsBackToRecencyWhenRepositoryHasNoEmbeddingSupport(t *testing.T) {
	// The base memoryRepository (used everywhere else in this file)
	// intentionally does not implement EmbeddingRepository — Search must
	// still work, falling back to ListApproved, exactly as it did before
	// this capability existed.
	manager, err := NewManager(NewService(nil), newMemoryRepository(), &recordingGate{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Search(context.Background(), SearchRequest{
		OrganizationID: "org", ActorRoleID: "role-a", RoleID: "role-a", QueryText: "anything",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSearchSkipsEmbeddingOnInsufficientBalanceAndStillReturnsResults(t *testing.T) {
	repo := newSearchableMemoryRepository()
	repo.searchResult = []Entry{{ID: "mem-fallback"}}
	ledger := &fakeEmbeddingLedger{balanceOK: false}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1}}
	manager, err := NewManager(NewService(nil), repo, &recordingGate{}, testSemanticDeps(t, repo, ledger, adapter, nil))
	if err != nil {
		t.Fatal(err)
	}
	results, err := manager.Search(context.Background(), SearchRequest{
		OrganizationID: "org", ActorRoleID: "role-a", RoleID: "role-a", QueryText: "reactor overheating",
	})
	if err != nil {
		t.Fatalf("Search must succeed with exact-only results even when the vector channel can't be afforded: %v", err)
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter entered=%d want 0 — must never call the provider after a rejected reservation", adapter.entered)
	}
	if len(results) != 1 || results[0].ID != "mem-fallback" {
		t.Fatalf("results=%+v", results)
	}
}
