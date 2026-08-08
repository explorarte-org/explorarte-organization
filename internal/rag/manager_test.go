package rag

import (
	"context"
	"errors"
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
	versions         map[string]KnowledgeVersion
	idempotency      map[string]string
	generations      map[string]IndexGeneration
	chunksByGen      map[string][]Chunk
	activeGeneration map[string]string
	queryResults     []QueryResult
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{versions: map[string]KnowledgeVersion{}, idempotency: map[string]string{}, generations: map[string]IndexGeneration{}, chunksByGen: map[string][]Chunk{}, activeGeneration: map[string]string{}}
}

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
	manager, err := NewManager(NewService(clock), repo, gate, namespaces)
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
	manager, err := NewManager(NewService(clock), repo, &recordingGate{}, &fakeNamespaces{})
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
