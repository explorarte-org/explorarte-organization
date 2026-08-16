package rag

import (
	"context"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/contentpolicy"
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
	vector      []float32
	err         error
	tokens      int64
	entered     int
	lastRequest embeddingruntime.EmbedRequest
}

func (a *fakeOnlineAdapter) Embed(_ context.Context, request embeddingruntime.EmbedRequest) (embeddingruntime.EmbedResponse, error) {
	a.entered++
	a.lastRequest = request
	if a.err != nil {
		return embeddingruntime.EmbedResponse{}, a.err
	}
	return embeddingruntime.EmbedResponse{
		Results:     []embeddingruntime.EmbedResult{{Key: request.Items[0].Key, Vector: a.vector}},
		InputTokens: a.tokens,
	}, nil
}

type fakeMediaFetcher struct {
	data       []byte
	err        error
	lastRef    string
	fetchCalls int
}

func (f *fakeMediaFetcher) FetchMedia(_ context.Context, ref string) ([]byte, error) {
	f.fetchCalls++
	f.lastRef = ref
	if f.err != nil {
		return nil, f.err
	}
	return f.data, nil
}

func testSemanticDeps(ledger *fakeEmbeddingLedger, adapter *fakeOnlineAdapter, budgets agentbudget.Ledger, t *testing.T) *SemanticSearchDeps {
	return &SemanticSearchDeps{
		OnlineAdapter: adapter, Pricing: newTestPricingService(t), Wallet: ledger, Budgets: budgets,
		ProviderID: "gemini", ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768, PromptTemplateVersion: "prompt-template.v1",
		Identity: EmbeddingIdentity{ModelID: "gemini-embedding-2", ModelVersion: "v1"},
	}
}

func newSemanticQueryManager(t *testing.T, semantic *SemanticSearchDeps) (*Manager, *fakeRepository) {
	t.Helper()
	repo := newFakeRepository()
	repo.activeGeneration["own:solo"] = "gen-1"
	repo.generations["gen-1"] = IndexGeneration{ID: "gen-1", Status: GenerationActive}
	namespaces := &fakeNamespaces{own: "solo"}
	manager, err := NewManager(NewService(nil), repo, &recordingGate{}, namespaces, semantic, nil)
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

	// AWS-style access key pattern — matched by contentpolicy as a
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

func TestEstimateTextTokens(t *testing.T) {
	if got := estimateTextTokens("abcd"); got != 2 {
		t.Fatalf("estimateTextTokens(4 bytes)=%d want 2", got)
	}
	if got := estimateTextTokens(""); got != 1 {
		t.Fatalf("estimateTextTokens(empty)=%d want 1", got)
	}
}

func TestEstimateMediaTokensPDFIncludesRenderedPagePlusText(t *testing.T) {
	got := estimateMediaTokens("application/pdf", "abcd")
	if want := int64(258 + 2); got != want {
		t.Fatalf("estimateMediaTokens(pdf)=%d want %d", got, want)
	}
}

func TestEstimateMediaTokensAudioUsesFlatPlaceholder(t *testing.T) {
	if got := estimateMediaTokens("audio/mpeg", "irrelevant"); got != 1000 {
		t.Fatalf("estimateMediaTokens(audio)=%d want 1000", got)
	}
}

func TestEmbedMediaSendsMimeTypeAndDataNotText(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.4, 0.5}, tokens: 300}
	manager, _ := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	vector := manager.embedMedia(context.Background(), "explorarte", "empresa/human", "extracted text", "application/pdf", []byte("%PDF-1.4"), nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationRAGReindex)
	if vector == nil {
		t.Fatal("expected a vector")
	}
	sent := adapter.lastRequest.Items[0]
	if sent.Text != "" {
		t.Fatalf("expected empty Text, got %q", sent.Text)
	}
	if sent.MimeType != "application/pdf" || string(sent.Data) != "%PDF-1.4" {
		t.Fatalf("sent item=%+v", sent)
	}
}

func TestEmbedMediaSkipsWhenClassifierTextMatchesForbiddenPattern(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.4, 0.5}, tokens: 300}
	manager, _ := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	// A credential-assignment pattern from the shared content policy —
	// even though this text is never itself the embed payload, it stands
	// in for the page's extracted text, which the media path still scans.
	vector := manager.embedMedia(context.Background(), "explorarte", "empresa/human", `api_key: "abcdefgh12345678"`, "application/pdf", []byte("%PDF-1.4"), nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationRAGReindex)
	if vector != nil {
		t.Fatal("expected embedMedia to skip when classifierText matches a forbidden pattern")
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter must never be called when the classifier text is rejected, entered=%d", adapter.entered)
	}
}

// TestEmbedMediaAllowsOrdinaryHealthcareVocabulary proves the organization
// does not infer a clinical record from words used in legitimate research.
func TestEmbedMediaAllowsOrdinaryHealthcareVocabulary(t *testing.T) {
	// "patient histories" as a memory-system example -- the exact shape of
	// false positive found in the real 16-paper audit corpus (MemOS, page
	// 13): ordinary engineering prose, not clinical data.
	pageText := "The system caches frequently accessed knowledge, such as patient histories, for fast retrieval during diagnosis."
	assessment := contentpolicy.Analyze(pageText)
	if assessment.HasCredentials() || len(assessment.Findings) != 0 {
		t.Fatalf("ordinary research vocabulary produced credential findings: %+v", assessment.Findings)
	}

	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.4, 0.5}, tokens: 300}
	manager, _ := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	vector := manager.embedMedia(context.Background(), "explorarte", "empresa/human", pageText, "application/pdf", []byte("%PDF-1.4"), nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationRAGReindex)
	if vector == nil {
		t.Fatal("expected embedMedia to proceed and return a vector for clinical-vocabulary (non-secret) text")
	}
	if adapter.entered != 1 {
		t.Fatalf("adapter called %d times, want 1 -- clinical vocabulary must reach the provider", adapter.entered)
	}
}

// TestEmbedMediaStillSkipsSecretShapedText proves the same fix did not
// weaken the real Organization concern the boundary is meant to preserve:
// secret-shaped content must still never reach the embedding provider.
// Reuses the same credential-assignment fixture as
// TestEmbedMediaSkipsWhenClassifierTextMatchesForbiddenPattern, but asserts
// the precondition through the real contentpolicy.Analyze first.
func TestEmbedMediaStillSkipsSecretShapedText(t *testing.T) {
	pageText := `api_key: "abcdefgh12345678"`
	assessment := contentpolicy.Analyze(pageText)
	if !assessment.HasCredentials() {
		t.Fatalf("test fixture does not exercise the credential path: findings=%+v", assessment.Findings)
	}

	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.4, 0.5}, tokens: 300}
	manager, _ := newSemanticQueryManager(t, testSemanticDeps(ledger, adapter, nil, t))

	vector := manager.embedMedia(context.Background(), "explorarte", "empresa/human", pageText, "application/pdf", []byte("%PDF-1.4"), nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationRAGReindex)
	if vector != nil {
		t.Fatal("expected embedMedia to skip secret-shaped classifier text")
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter must never be called for secret-shaped text, entered=%d", adapter.entered)
	}
}
