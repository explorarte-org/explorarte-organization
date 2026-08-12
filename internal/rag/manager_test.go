package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type recordingGate struct {
	requests []AuthorizationRequest
	err      error
}

func (g *recordingGate) Authorize(_ context.Context, request AuthorizationRequest) error {
	g.requests = append(g.requests, request)
	return g.err
}

type fakeNamespaces struct {
	own        string
	department string
	err        error
}

func (f *fakeNamespaces) ResolveNamespace(_ context.Context, _, _ string, kind NamespaceKind) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if kind == NamespaceOwn {
		return f.own, nil
	}
	return f.department, nil
}

type fakeRepository struct {
	versions             map[string]KnowledgeVersion
	idempotency          map[string]string
	generations          map[string]IndexGeneration
	chunksByGen          map[string][]Chunk
	activeGeneration     map[string]string
	queryResults         []QueryResult
	lastQueryCommand     QueryCommand
	chunkEmbeddings      map[string]ChunkEmbedding
	bgeM3ChunkEmbeddings map[string]BGEM3ChunkEmbedding
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		versions: map[string]KnowledgeVersion{}, idempotency: map[string]string{}, generations: map[string]IndexGeneration{},
		chunksByGen: map[string][]Chunk{}, activeGeneration: map[string]string{},
		chunkEmbeddings: map[string]ChunkEmbedding{}, bgeM3ChunkEmbeddings: map[string]BGEM3ChunkEmbedding{},
	}
}

// PendingChunkEmbeddings mirrors postgres's real query well enough for
// Manager.BackfillEmbeddings unit tests: chunks in namespaceKind/
// namespaceID's active generation that have no row yet in whichever fake
// map identity's shape selects.
func (r *fakeRepository) PendingChunkEmbeddings(_ context.Context, _ string, namespaceKind NamespaceKind, namespaceID string, identity EmbeddingIdentity, limit int) ([]Chunk, error) {
	scope := string(namespaceKind) + ":" + namespaceID
	generationID, ok := r.activeGeneration[scope]
	if !ok {
		return nil, nil
	}
	pending := make([]Chunk, 0)
	for _, chunk := range r.chunksByGen[generationID] {
		embedded := false
		if identity.ModelVersion != "" {
			_, embedded = r.chunkEmbeddings[chunk.ID]
		} else {
			_, embedded = r.bgeM3ChunkEmbeddings[chunk.ID]
		}
		if embedded {
			continue
		}
		pending = append(pending, chunk)
		if len(pending) >= limit {
			break
		}
	}
	return pending, nil
}

func (r *fakeRepository) InsertChunkEmbedding(_ context.Context, embedding ChunkEmbedding) error {
	r.chunkEmbeddings[embedding.ChunkID] = embedding
	return nil
}
func (r *fakeRepository) NearestChunks(context.Context, string, string, []float32, int) ([]ScoredChunk, error) {
	return nil, nil
}
func (r *fakeRepository) CreateEmbeddingBatchJob(context.Context, EmbeddingBatchJob, []EmbeddingBatchJobItem) (EmbeddingBatchJob, error) {
	return EmbeddingBatchJob{}, nil
}
func (r *fakeRepository) RecordEmbeddingBatchJobItemResult(context.Context, int64, string, *ChunkEmbedding, string) error {
	return nil
}
func (r *fakeRepository) CompleteEmbeddingBatchJob(context.Context, int64, string, time.Time, int) error {
	return nil
}
func (r *fakeRepository) InsertBGEM3ChunkEmbedding(_ context.Context, embedding BGEM3ChunkEmbedding) error {
	r.bgeM3ChunkEmbeddings[embedding.ChunkID] = embedding
	return nil
}
func (r *fakeRepository) NearestBGEM3Chunks(context.Context, string, string, []float32, int) ([]ScoredChunk, error) {
	return nil, nil
}

var (
	_ EmbeddingBackfillRepository = (*fakeRepository)(nil)
	_ EmbeddingRepository         = (*fakeRepository)(nil)
	_ BGEM3EmbeddingRepository    = (*fakeRepository)(nil)
)

func (r *fakeRepository) CreateCandidate(_ context.Context, command CreateCandidateCommand) (KnowledgeVersion, bool, error) {
	if existingID, ok := r.idempotency[command.IdempotencyKey]; ok {
		return r.versions[existingID], true, nil
	}
	r.versions[command.Version.ID] = command.Version
	r.idempotency[command.IdempotencyKey] = command.Version.ID
	return command.Version, false, nil
}

func (r *fakeRepository) Get(_ context.Context, _, versionID string) (KnowledgeVersion, error) {
	version, ok := r.versions[versionID]
	if !ok {
		return KnowledgeVersion{}, ErrNotFound
	}
	return version, nil
}

func (r *fakeRepository) Save(_ context.Context, command SaveCommand) (KnowledgeVersion, error) {
	current, ok := r.versions[command.Version.ID]
	if !ok {
		return KnowledgeVersion{}, ErrNotFound
	}
	if current.Revision != command.ExpectedRevision {
		return KnowledgeVersion{}, ErrRevisionConflict
	}
	r.versions[command.Version.ID] = command.Version
	return command.Version, nil
}

func (r *fakeRepository) List(_ context.Context, filter ListFilter) ([]KnowledgeVersion, error) {
	values := []KnowledgeVersion{}
	for _, version := range r.versions {
		if filter.OrganizationID != "" && version.OrganizationID != filter.OrganizationID {
			continue
		}
		if filter.NamespaceKind != "" && version.NamespaceKind != filter.NamespaceKind {
			continue
		}
		if filter.NamespaceID != "" && version.NamespaceID != filter.NamespaceID {
			continue
		}
		if filter.Lifecycle != "" && version.Lifecycle != filter.Lifecycle {
			continue
		}
		values = append(values, version)
	}
	return values, nil
}

func (r *fakeRepository) ApprovedForNamespace(ctx context.Context, organizationID string, namespaceKind NamespaceKind, namespaceID string) ([]KnowledgeVersion, error) {
	return r.List(ctx, ListFilter{OrganizationID: organizationID, NamespaceKind: namespaceKind, NamespaceID: namespaceID, Lifecycle: LifecycleApproved})
}

func (r *fakeRepository) Reindex(_ context.Context, command ReindexCommand) (IndexGeneration, error) {
	scope := string(command.NamespaceKind) + ":" + command.NamespaceID
	generationID := scope + "-1"
	generation := IndexGeneration{ID: generationID, OrganizationID: command.OrganizationID, NamespaceKind: command.NamespaceKind, NamespaceID: command.NamespaceID, Generation: 1, Status: GenerationActive, ChunkerID: command.ChunkerID, ChunkerVersion: command.ChunkerVersion, CreatedAt: time.Now()}
	r.generations[generationID] = generation
	r.chunksByGen[generationID] = command.Chunks
	r.activeGeneration[scope] = generationID
	return generation, nil
}

func (r *fakeRepository) Query(_ context.Context, command QueryCommand) ([]QueryResult, error) {
	r.lastQueryCommand = command
	return r.queryResults, nil
}

func (r *fakeRepository) ActiveGeneration(_ context.Context, organizationID string, namespaceKind NamespaceKind, namespaceID string) (IndexGeneration, bool, error) {
	scope := string(namespaceKind) + ":" + namespaceID
	id, ok := r.activeGeneration[scope]
	if !ok {
		return IndexGeneration{}, false, nil
	}
	return r.generations[id], true, nil
}

func (r *fakeRepository) ExistingEvidenceReferences(_ context.Context, _, referencePrefix string) (map[string]bool, error) {
	existing := make(map[string]bool)
	for _, version := range r.versions {
		for _, ref := range version.EvidenceRefs {
			if strings.HasPrefix(ref.Reference, referencePrefix) {
				existing[ref.Reference] = true
			}
		}
	}
	return existing, nil
}

func newTestManager(t *testing.T, gate AuthorizationGate, namespaces NamespaceResolver) (*Manager, *fixedClock, *fakeRepository) {
	t.Helper()
	clock := &fixedClock{now: time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)}
	repo := newFakeRepository()
	manager, err := NewManager(NewService(clock), repo, gate, namespaces, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager, clock, repo
}

func TestManagerProposeIsIdempotentAndAlwaysAuthorized(t *testing.T) {
	gate := &recordingGate{}
	manager, clock, _ := newTestManager(t, gate, &fakeNamespaces{})
	ctx := context.Background()
	command := validProposeCommand(clock.now)
	first, reused1, err := manager.Propose(ctx, ProposeRequest{Command: command, IdempotencyKey: "key-1"})
	if err != nil || reused1 {
		t.Fatalf("first propose=%+v reused=%v err=%v", first, reused1, err)
	}
	second, reused2, err := manager.Propose(ctx, ProposeRequest{Command: command, IdempotencyKey: "key-1"})
	if err != nil || !reused2 || second.ID != first.ID {
		t.Fatalf("second propose=%+v reused=%v err=%v", second, reused2, err)
	}
	if len(gate.requests) != 2 || gate.requests[0].CapabilityID != CapabilityPropose {
		t.Fatalf("expected propose authorization on every attempt, got %+v", gate.requests)
	}
}

func TestManagerReviewRejectsStaleRevision(t *testing.T) {
	gate := &recordingGate{}
	manager, clock, _ := newTestManager(t, gate, &fakeNamespaces{})
	ctx := context.Background()
	version, _, err := manager.Propose(ctx, ProposeRequest{Command: validProposeCommand(clock.now), IdempotencyKey: "key-1"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	_, err = manager.Review(ctx, ReviewRequest{Mutation: MutationRequest{OrganizationID: version.OrganizationID, VersionID: version.ID, ExpectedRevision: 99, ActorRoleID: "empresa/human", Reason: "stale"}, Outcome: ReviewApprove})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision err=%v", err)
	}
}

func TestManagerFullLifecycleAndReindexAndQuery(t *testing.T) {
	gate := &recordingGate{}
	manager, clock, repo := newTestManager(t, gate, &fakeNamespaces{own: "investigacion/research_worker_hourly", department: "ingenieria_ia"})
	ctx := context.Background()
	version, _, err := manager.Propose(ctx, ProposeRequest{Command: validProposeCommand(clock.now), IdempotencyKey: "key-1"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	approved, err := manager.Review(ctx, ReviewRequest{Mutation: MutationRequest{OrganizationID: version.OrganizationID, VersionID: version.ID, ExpectedRevision: 1, ActorRoleID: "empresa/human", Reason: "looks correct"}, Outcome: ReviewApprove})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Lifecycle != LifecycleApproved {
		t.Fatalf("approved=%+v", approved)
	}
	generation, err := manager.Reindex(ctx, ReindexRequest{OrganizationID: approved.OrganizationID, NamespaceKind: approved.NamespaceKind, NamespaceID: approved.NamespaceID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	if generation.Status != GenerationActive {
		t.Fatalf("generation=%+v", generation)
	}
	chunks := repo.chunksByGen[generation.ID]
	if len(chunks) == 0 {
		t.Fatal("reindex produced no chunks")
	}

	repo.queryResults = []QueryResult{{Chunk: chunks[0], DocumentID: approved.DocumentID, NamespaceKind: approved.NamespaceKind, NamespaceID: approved.NamespaceID, DataClass: approved.Admission.DataClass, GenerationID: generation.ID, Score: 1}}
	results, err := manager.Query(ctx, QueryRequest{OrganizationID: approved.OrganizationID, ActorRoleID: "ingenieria_ia/orquestador", Scope: NamespaceDepartment, QueryText: "riesgos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%+v", results)
	}
	readRequests := 0
	for _, request := range gate.requests {
		if request.CapabilityID == CapabilityReadDepartment {
			readRequests++
		}
	}
	if readRequests != 1 {
		t.Fatalf("expected department read authorization to run, gate saw %+v", gate.requests)
	}
}

func TestManagerGetAuthorizesPersistedNamespace(t *testing.T) {
	gate := &recordingGate{}
	manager, clock, repo := newTestManager(t, gate, &fakeNamespaces{department: "ingenieria_ia"})
	version, err := NewService(clock).Propose(validProposeCommand(clock.now))
	if err != nil {
		t.Fatal(err)
	}
	repo.versions[version.ID] = version

	got, err := manager.Get(context.Background(), version.OrganizationID, version.ID, "ingenieria_ia/orquestador")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != version.ID || len(gate.requests) != 1 {
		t.Fatalf("got=%+v authorization=%+v", got, gate.requests)
	}
	request := gate.requests[0]
	if request.CapabilityID != CapabilityReadDepartment || request.ResourceID != "department:ingenieria_ia" {
		t.Fatalf("authorization=%+v", request)
	}
}

func TestManagerGetRejectsHorizontalNamespaceRead(t *testing.T) {
	gate := &recordingGate{}
	manager, clock, repo := newTestManager(t, gate, &fakeNamespaces{own: "ingenieria_ia/frontend"})
	command := validProposeCommand(clock.now)
	command.NamespaceKind = NamespaceOwn
	command.NamespaceID = "ingenieria_ia/backend"
	version, err := NewService(clock).Propose(command)
	if err != nil {
		t.Fatal(err)
	}
	repo.versions[version.ID] = version

	_, err = manager.Get(context.Background(), version.OrganizationID, version.ID, "ingenieria_ia/frontend")
	if !errors.Is(err, ErrInvalidNamespace) {
		t.Fatalf("horizontal read err=%v", err)
	}
	if len(gate.requests) != 0 {
		t.Fatalf("mismatched namespace reached authorization gate: %+v", gate.requests)
	}
}

func TestManagerListRequiresAndAuthorizesExplicitNamespace(t *testing.T) {
	gate := &recordingGate{}
	manager, clock, repo := newTestManager(t, gate, &fakeNamespaces{department: "ingenieria_ia"})
	version, err := NewService(clock).Propose(validProposeCommand(clock.now))
	if err != nil {
		t.Fatal(err)
	}
	repo.versions[version.ID] = version

	if _, err := manager.List(context.Background(), "ingenieria_ia/orquestador", ListFilter{OrganizationID: version.OrganizationID}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unscoped list err=%v", err)
	}
	values, err := manager.List(context.Background(), "ingenieria_ia/orquestador", ListFilter{
		OrganizationID: version.OrganizationID, NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || len(gate.requests) != 1 || gate.requests[0].CapabilityID != CapabilityReadDepartment {
		t.Fatalf("values=%+v authorization=%+v", values, gate.requests)
	}
}

func TestManagerGetForRevalidationDoesNotInvokeActorGate(t *testing.T) {
	gate := &recordingGate{err: errors.New("must not be called")}
	manager, clock, repo := newTestManager(t, gate, &fakeNamespaces{})
	version, err := NewService(clock).Propose(validProposeCommand(clock.now))
	if err != nil {
		t.Fatal(err)
	}
	repo.versions[version.ID] = version
	got, err := manager.GetForRevalidation(context.Background(), version.OrganizationID, version.ID)
	if err != nil || got.ID != version.ID || len(gate.requests) != 0 {
		t.Fatalf("got=%+v err=%v authorization=%+v", got, err, gate.requests)
	}
}

// misbehavingApprovedRepository simulates a repository bug where
// ApprovedForNamespace returns a version that is not actually approved, to
// prove the manager enforces "approved-only indexing" itself rather than
// trusting the repository layer as the only safety net.
type misbehavingApprovedRepository struct {
	*fakeRepository
	version KnowledgeVersion
}

func (r misbehavingApprovedRepository) ApprovedForNamespace(context.Context, string, NamespaceKind, string) ([]KnowledgeVersion, error) {
	return []KnowledgeVersion{r.version}, nil
}

func TestManagerReindexRejectsNonApprovedVersion(t *testing.T) {
	clock := &fixedClock{now: time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)}
	candidate, err := NewService(clock).Propose(validProposeCommand(clock.now))
	if err != nil {
		t.Fatal(err)
	}
	repo := misbehavingApprovedRepository{fakeRepository: newFakeRepository(), version: candidate}
	manager, err := NewManager(NewService(clock), repo, &recordingGate{}, &fakeNamespaces{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reindex(context.Background(), ReindexRequest{OrganizationID: candidate.OrganizationID, NamespaceKind: candidate.NamespaceKind, NamespaceID: candidate.NamespaceID, ActorRoleID: "empresa/human"}); !errors.Is(err, ErrVersionNotApproved) {
		t.Fatalf("reindex of candidate err=%v", err)
	}
}

func TestManagerQueryDeniesWhenNamespaceResolutionFails(t *testing.T) {
	gate := &recordingGate{}
	manager, _, _ := newTestManager(t, gate, &fakeNamespaces{err: ErrInvalidNamespace})
	if _, err := manager.Query(context.Background(), QueryRequest{OrganizationID: "explorarte", ActorRoleID: "ingenieria_ia/orquestador", Scope: NamespaceOwn, QueryText: "riesgos"}); !errors.Is(err, ErrInvalidNamespace) {
		t.Fatalf("namespace resolution failure err=%v", err)
	}
}

func TestManagerPropagatesAuthorizationDenial(t *testing.T) {
	gate := &recordingGate{err: errors.New("denied")}
	manager, clock, _ := newTestManager(t, gate, &fakeNamespaces{})
	if _, _, err := manager.Propose(context.Background(), ProposeRequest{Command: validProposeCommand(clock.now), IdempotencyKey: "key-1"}); err == nil {
		t.Fatal("expected authorization denial to propagate")
	}
}

func newBackfillTestManager(t *testing.T, semantic *SemanticSearchDeps, chunkCount int) (*Manager, *fakeRepository, *recordingGate) {
	t.Helper()
	repo := newFakeRepository()
	repo.activeGeneration["department:ingenieria_ia"] = "gen-1"
	repo.generations["gen-1"] = IndexGeneration{ID: "gen-1", Status: GenerationActive}
	chunks := make([]Chunk, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		chunks = append(chunks, Chunk{ID: fmt.Sprintf("chunk-%d", i), GenerationID: "gen-1", Content: fmt.Sprintf("content %d", i)})
	}
	repo.chunksByGen["gen-1"] = chunks
	gate := &recordingGate{}
	manager, err := NewManager(NewService(nil), repo, gate, &fakeNamespaces{}, semantic, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager, repo, gate
}

func TestManagerBackfillEmbeddingsRequiresSemanticConfigured(t *testing.T) {
	manager, _, _ := newBackfillTestManager(t, nil, 3)
	_, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v want ErrInvalidRequest", err)
	}
}

func TestManagerBackfillEmbeddingsPropagatesAuthorizationDenial(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	manager, _, gate := newBackfillTestManager(t, testSemanticDeps(ledger, adapter, nil, t), 3)
	gate.err = errors.New("denied")
	if _, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human",
	}); err == nil {
		t.Fatal("expected authorization denial to propagate")
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter entered=%d want 0 — must never embed before authorization succeeds", adapter.entered)
	}
}

func TestManagerBackfillEmbeddingsEmbedsPendingChunksAndReportsDone(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	manager, repo, _ := newBackfillTestManager(t, testSemanticDeps(ledger, adapter, nil, t), 3)

	result, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 3 || result.Skipped != 0 || !result.Done {
		t.Fatalf("result=%+v want Embedded=3 Skipped=0 Done=true", result)
	}
	if len(repo.chunkEmbeddings) != 3 {
		t.Fatalf("chunkEmbeddings=%d want 3", len(repo.chunkEmbeddings))
	}

	// A second call finds nothing left pending — proving the "already has a
	// row" check actually works, not just that the first call didn't error.
	result, err = manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 0 || !result.Done {
		t.Fatalf("second call result=%+v want Embedded=0 Done=true", result)
	}
}

func TestManagerBackfillEmbeddingsPagesWithBatchSizeAndDoneFlag(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	manager, _, _ := newBackfillTestManager(t, testSemanticDeps(ledger, adapter, nil, t), 5)

	first, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Embedded != 2 || first.Done {
		t.Fatalf("first page=%+v want Embedded=2 Done=false", first)
	}
	second, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Embedded != 2 || second.Done {
		t.Fatalf("second page=%+v want Embedded=2 Done=false", second)
	}
	third, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Embedded != 1 || !third.Done {
		t.Fatalf("third page=%+v want Embedded=1 Done=true", third)
	}
}

func TestManagerBackfillEmbeddingsSkipsChunksThatFailToEmbedWithoutFailingTheCall(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{err: errors.New("provider unavailable")}
	manager, repo, _ := newBackfillTestManager(t, testSemanticDeps(ledger, adapter, nil, t), 2)

	result, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 0 || result.Skipped != 2 || !result.Done {
		t.Fatalf("result=%+v want Embedded=0 Skipped=2 Done=true", result)
	}
	if len(repo.chunkEmbeddings) != 0 {
		t.Fatalf("chunkEmbeddings=%d want 0 — a failed embed must never be inserted", len(repo.chunkEmbeddings))
	}
}

func TestManagerBackfillEmbeddingsSelectsBGEM3TableForBGEM3Identity(t *testing.T) {
	adapter := &fakeOnlineAdapter{vector: make([]float32, 1024), tokens: 5}
	semantic := &SemanticSearchDeps{
		OnlineAdapter: adapter, LocalComputeOnly: true,
		ProviderID: "bge-m3-local", ProviderModelID: "bge-m3-2024-06", OutputDimensionality: 1024, PromptTemplateVersion: "bge-m3-prompt-template.v1",
		Identity: EmbeddingIdentity{
			ModelID: "bge-m3-local", ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("d", 64),
			TokenizerRevision: "bge-m3-tokenizer-2024-06", Normalization: "l2", Pooling: "cls",
		},
	}
	manager, repo, _ := newBackfillTestManager(t, semantic, 2)

	result, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human", BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 2 || !result.Done {
		t.Fatalf("result=%+v want Embedded=2 Done=true", result)
	}
	if len(repo.bgeM3ChunkEmbeddings) != 2 || len(repo.chunkEmbeddings) != 0 {
		t.Fatalf("bgeM3ChunkEmbeddings=%d chunkEmbeddings=%d want 2 and 0 — must never write the gemini table for a bge-m3 identity", len(repo.bgeM3ChunkEmbeddings), len(repo.chunkEmbeddings))
	}
}

func newBackfillTestManagerWithMedia(t *testing.T, semantic *SemanticSearchDeps, mediaFetcher MediaFetcher, chunks []Chunk) (*Manager, *fakeRepository, *recordingGate) {
	t.Helper()
	repo := newFakeRepository()
	repo.activeGeneration["department:ingenieria_ia"] = "gen-1"
	repo.generations["gen-1"] = IndexGeneration{ID: "gen-1", Status: GenerationActive}
	repo.chunksByGen["gen-1"] = chunks
	gate := &recordingGate{}
	manager, err := NewManager(NewService(nil), repo, gate, &fakeNamespaces{}, semantic, mediaFetcher)
	if err != nil {
		t.Fatal(err)
	}
	return manager, repo, gate
}

func TestManagerBackfillEmbeddingsEmbedsMediaBackedChunkViaMediaFetcher(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	fetcher := &fakeMediaFetcher{data: []byte("%PDF-1.4 fake page bytes")}
	chunks := []Chunk{{
		ID: "chunk-pdf-1", GenerationID: "gen-1", Content: "extracted page text",
		MediaSourceRef: "raw/papers/foo.pdf-page-3.pdf", MediaMimeType: "application/pdf",
	}}
	manager, _, _ := newBackfillTestManagerWithMedia(t, testSemanticDeps(ledger, adapter, nil, t), fetcher, chunks)

	result, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 1 || result.Skipped != 0 {
		t.Fatalf("result=%+v", result)
	}
	if fetcher.fetchCalls != 1 || fetcher.lastRef != "raw/papers/foo.pdf-page-3.pdf" {
		t.Fatalf("fetcher calls=%d lastRef=%q", fetcher.fetchCalls, fetcher.lastRef)
	}
	sentItem := adapter.lastRequest.Items[0]
	if sentItem.Text != "" {
		t.Fatalf("expected empty Text for media item, got %q", sentItem.Text)
	}
	if sentItem.MimeType != "application/pdf" || string(sentItem.Data) != "%PDF-1.4 fake page bytes" {
		t.Fatalf("sent item=%+v", sentItem)
	}
}

func TestManagerBackfillEmbeddingsSkipsMediaChunkWithNoMediaFetcherConfigured(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	chunks := []Chunk{{ID: "chunk-pdf-1", GenerationID: "gen-1", Content: "text", MediaSourceRef: "raw/x.pdf", MediaMimeType: "application/pdf"}}
	manager, _, _ := newBackfillTestManagerWithMedia(t, testSemanticDeps(ledger, adapter, nil, t), nil, chunks)

	result, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 0 || result.Skipped != 1 {
		t.Fatalf("result=%+v, want 0 embedded 1 skipped", result)
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter must never be called without a media fetcher, entered=%d", adapter.entered)
	}
}

func TestManagerBackfillEmbeddingsSkipsMediaChunkWhenFetchFails(t *testing.T) {
	ledger := &fakeEmbeddingLedger{balanceOK: true}
	adapter := &fakeOnlineAdapter{vector: []float32{0.1, 0.2}, tokens: 5}
	fetcher := &fakeMediaFetcher{err: errors.New("object storage unavailable")}
	chunks := []Chunk{{ID: "chunk-pdf-1", GenerationID: "gen-1", Content: "text", MediaSourceRef: "raw/x.pdf", MediaMimeType: "application/pdf"}}
	manager, _, _ := newBackfillTestManagerWithMedia(t, testSemanticDeps(ledger, adapter, nil, t), fetcher, chunks)

	result, err := manager.BackfillEmbeddings(context.Background(), BackfillEmbeddingsRequest{
		OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia", ActorRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded != 0 || result.Skipped != 1 {
		t.Fatalf("result=%+v, want 0 embedded 1 skipped", result)
	}
	if adapter.entered != 0 {
		t.Fatalf("adapter must never be called when the fetch itself failed, entered=%d", adapter.entered)
	}
}

func TestChunkIsMedia(t *testing.T) {
	if (Chunk{Content: "hola"}).IsMedia() {
		t.Fatal("text-only chunk must not report IsMedia")
	}
	if !(Chunk{MediaSourceRef: "raw/x.pdf", MediaMimeType: "application/pdf"}).IsMedia() {
		t.Fatal("media-backed chunk must report IsMedia")
	}
}
