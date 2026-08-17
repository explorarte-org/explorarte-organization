package contextengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

func TestServiceBuildIsIdempotentAndSnapshotIsImmutable(t *testing.T) {
	fixture := newServiceFixture(t)
	request := fixture.request("idem-1")
	first, err := fixture.service.Build(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Build(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || !second.Reused || first.Snapshot.ID != second.Snapshot.ID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	request.Purpose = "different"
	if _, err = fixture.service.Build(t.Context(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict=%v", err)
	}
	stored, err := fixture.service.Get(t.Context(), first.Snapshot.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RenderedHash == "" || stored.SegmentCount == 0 {
		t.Fatalf("stored=%+v", stored)
	}
}

// TestServiceBuild_HistoricalSelectorBindingThenContradiction is
// independent review round 2's required proof
// (REAL_HISTORICAL_CONTEXT_BINDING_PROOF /
// POST_RESTART_SELECTOR_CONTRADICTION_PROOF): a snapshot whose durable
// selector facts are all empty (exactly what every real pre-M1.3
// context_snapshots row is, since migration 000053 never backfills
// them) does NOT remain permanently unbound. The first resumed request
// that supplies real selector values is accepted AND durably binds them
// (Store.BindSelectorFacts); a LATER, DIFFERENT resumed request under the
// SAME idempotency key is then compared against that now-concrete value
// and correctly fails closed, instead of also being silently accepted
// the way an unconditionally-empty comparison would allow forever.
func TestServiceBuild_HistoricalSelectorBindingThenContradiction(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := t.Context()
	// reconcileSelectorFacts only reads s.store -- exercised directly
	// here (not through the full Build() pipeline) so this test is not
	// coupled to reproducing resolve()/assemble()'s exact resolved
	// Sources just to make a hash match; that machinery is already
	// covered by TestServiceBuildIsIdempotentAndSnapshotIsImmutable and
	// the rest of this file. What is under test here is the
	// idempotency-reuse decision and binding behavior itself.
	svc := &contextService{store: fixture.store}

	historical := BuildRequest{OrganizationID: "explorarte", OrganizationRevisionID: 1, ActorRoleID: "ingenieria_ia/qa", Purpose: "unit test", IdempotencyKey: "historical-binding"}
	base := CanonicalBuildRequest{Request: historical, PrecedenceHash: DigestMarkdown([]byte("p")), CanonicalBundleHash: DigestMarkdown([]byte("c"))}
	// A genuine pre-M1.3 row's stored hash is legacy-shaped -- computed
	// before TaskClass/ExecutionPurpose/ActorUnitID were ever written to
	// the buffer at all, never DigestBuildRequest with those three fields
	// merely left empty (that is a structurally different, always-
	// divergent shape -- see digestBuildRequestLegacy's own doc comment).
	legacyHash := digestBuildRequestLegacy(base)
	seedID, err := fixture.store.AllocateID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed := Snapshot{
		ID: seedID, OrganizationID: historical.OrganizationID, OrganizationRevisionID: historical.OrganizationRevisionID,
		ActorRoleID: historical.ActorRoleID, Purpose: historical.Purpose, IdempotencyKey: historical.IdempotencyKey,
		Status: SnapshotReady, Version: 1, RequestHash: legacyHash, RenderedHash: DigestMarkdown([]byte("historical render")),
		CreatedAt: time.Now().UTC(),
	}
	createResult, err := fixture.store.Create(ctx, CreateSnapshotCommand{Snapshot: seed, Now: seed.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	first := createResult.Snapshot
	if first.TaskClass != "" || first.ExecutionPurpose != "" || first.ActorUnitID != "" {
		t.Fatalf("test setup bug: expected an empty-selector snapshot to simulate a pre-M1.3 row, got %+v", first)
	}

	// The first resumed request to supply real selector facts is accepted
	// (existing values are empty -- no assertion to contradict) and binds
	// them durably.
	resumed := base
	resumed.Request.TaskClass, resumed.Request.ExecutionPurpose, resumed.Request.ActorUnitID = "research.corpus_curate", "department-worker", "investigacion"
	bound, err := svc.reconcileSelectorFacts(ctx, first, DigestBuildRequest(resumed), resumed)
	if err != nil {
		t.Fatalf("first resumed request with real selector facts must be accepted: %v", err)
	}
	if bound.ID != first.ID {
		t.Fatalf("expected the same snapshot to be reused, got %+v", bound)
	}
	if bound.TaskClass != "research.corpus_curate" || bound.ExecutionPurpose != "department-worker" || bound.ActorUnitID != "investigacion" {
		t.Fatalf("resumed request did not durably bind the selector facts onto the historical snapshot: %+v", bound)
	}

	// Reloading the snapshot independently (simulating a fresh
	// read/process restart) proves the binding is durable, not merely
	// reflected in this one call's in-memory return value.
	reloaded, err := fixture.store.Get(ctx, first.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TaskClass != "research.corpus_curate" || reloaded.ExecutionPurpose != "department-worker" || reloaded.ActorUnitID != "investigacion" {
		t.Fatalf("bound selector facts did not survive an independent reload: %+v", reloaded)
	}

	// A LATER, DIFFERENT resumed request under the same idempotency key
	// must now fail closed against the durably bound value -- this is the
	// exact gap round 2 found: before binding, this would have been
	// silently accepted too, forever, since the comparison always saw an
	// empty existing value.
	contradictory := base
	contradictory.Request.TaskClass, contradictory.Request.ExecutionPurpose, contradictory.Request.ActorUnitID = "coordination.ceo_plan", "ceo-plan", "empresa"
	if _, err := svc.reconcileSelectorFacts(ctx, reloaded, DigestBuildRequest(contradictory), contradictory); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict for a selector tuple contradicting the now-bound value, got %v", err)
	}

	// The exact bound value remains accepted as ordinary idempotent reuse.
	matching := base
	matching.Request.TaskClass, matching.Request.ExecutionPurpose, matching.Request.ActorUnitID = "research.corpus_curate", "department-worker", "investigacion"
	again, err := svc.reconcileSelectorFacts(ctx, reloaded, DigestBuildRequest(matching), matching)
	if err != nil || again.ID != first.ID {
		t.Fatalf("exact matching resumed request must remain accepted: result=%+v err=%v", again, err)
	}
}

func TestServiceBuildRejectsRoleStatesAndAllowsHumanOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*registry.Role)
		code   ReasonCode
	}{
		{"disabled", func(r *registry.Role) { r.Enabled = false }, ReasonRoleDisabled},
		{"retired", func(r *registry.Role) { now := time.Now(); r.RetiredAt = &now }, ReasonRoleRetired},
		{"non-executable", func(r *registry.Role) { r.Executable = false }, ReasonRoleNotExecutable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			r := f.registry.role
			tc.mutate(&r)
			f.registry.role = r
			_, err := f.service.Build(t.Context(), f.request(tc.name))
			if ReasonOf(err) != tc.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
	f := newServiceFixture(t)
	f.registry.role.ID = "empresa/human"
	f.registry.role.RoleSlug = "human"
	f.registry.role.UnitID = "empresa"
	f.registry.role.Executable = false
	f.registry.organization.OwnerRoleID = "empresa/human"
	f.registry.unit.ID = "empresa"
	f.registry.unit.AgentPath = pointer("empresa/AGENT.md")
	f.docs.docs["empresa/AGENT.md"] = agentDoc("empresa/AGENT.md")
	f.docs.docs["empresa/human/PERFIL.md"] = profileDoc("empresa/human/PERFIL.md", "empresa", "human", "empresa")
	p := "empresa/human/PERFIL.md"
	d := "empresa/AGENT.md"
	m := "empresa"
	f.registry.role.ProfilePath = &p
	f.registry.role.DepartmentAgentPath = &d
	f.registry.role.MemoryDomain = &m
	req := f.request("owner")
	req.ActorRoleID = "empresa/human"
	if _, err := f.service.Build(t.Context(), req); err != nil {
		t.Fatalf("owner build=%v", err)
	}
}

func TestServiceProviderUnavailableIsOperationalForRequestedProject(t *testing.T) {
	f := newServiceFixture(t)
	request := f.request("project")
	request.ProjectRef = "project-1"
	_, err := f.service.Build(t.Context(), request)
	if !errors.Is(err, ErrSourceProviderUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestServiceValidateDetectsProfileAndCanonicalDriftWithoutMutatingSnapshot(t *testing.T) {
	f := newServiceFixture(t)
	result, err := f.service.Build(t.Context(), f.request("drift"))
	if err != nil {
		t.Fatal(err)
	}
	profile := f.docs.docs["ingenieria_ia/qa/PERFIL.md"]
	profile.Normalized = []byte("changed")
	profile.Hash = DigestMarkdown(profile.Normalized)
	f.docs.docs[profile.Path] = profile
	validation, err := f.service.Validate(t.Context(), result.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Valid || validation.ReasonCode != string(ReasonSnapshotStale) {
		t.Fatalf("validation=%+v", validation)
	}
	stored, err := f.service.Get(t.Context(), result.Snapshot.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != SnapshotReady {
		t.Fatalf("validation mutated status=%s", stored.Status)
	}
}

func TestServiceInvalidationIsTerminalAndRenderRejectsIt(t *testing.T) {
	f := newServiceFixture(t)
	result, err := f.service.Build(t.Context(), f.request("invalidate"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.service.Invalidate(t.Context(), InvalidateCommand{SnapshotID: result.Snapshot.ID, ActorRoleID: "ingenieria_ia/qa", Reason: "source superseded"})
	if err != nil {
		t.Fatal(err)
	}
	if value.Status != SnapshotInvalidated {
		t.Fatalf("status=%s", value.Status)
	}
	if _, err = f.service.Render(t.Context(), value.ID); !errors.Is(err, ErrSnapshotInvalidated) {
		t.Fatalf("render err=%v", err)
	}
}

func TestServicePreservesContextCancellation(t *testing.T) {
	f := newServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.service.Build(ctx, f.request("cancelled"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

type serviceFixture struct {
	service   Service
	registry  *fakeRegistry
	docs      *fakeDocuments
	store     *memoryStore
	canonical *fakeCanonical
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	profilePath := "ingenieria_ia/qa/PERFIL.md"
	agentPath := "ingenieria_ia/AGENT.md"
	memory := "ingenieria_ia"
	reg := &fakeRegistry{organization: registry.Organization{ID: "explorarte", OwnerRoleID: "empresa/human", CurrentRevision: 1}, revision: &registry.Revision{ID: 1, CanonicalHash: DigestMarkdown([]byte("registry"))}, unit: registry.Unit{OrganizationID: "explorarte", ID: "ingenieria_ia", AgentPath: &agentPath}, role: registry.Role{OrganizationID: "explorarte", ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", RoleSlug: "qa", ProfilePath: &profilePath, DepartmentAgentPath: &agentPath, MemoryDomain: &memory, Enabled: true, Executable: true, SourceRevision: 1}}
	docs := &fakeDocuments{docs: map[string]LoadedDocument{"AGENT.md": agentDoc("AGENT.md"), agentPath: agentDoc(agentPath), profilePath: profileDoc(profilePath, "ingenieria_ia", "qa", "ingenieria_ia")}}
	canonical := &fakeCanonical{bundle: CanonicalBundle{PrecedenceHash: DigestMarkdown([]byte("precedence")), BundleHash: DigestMarkdown([]byte("bundle")), Sources: []CanonicalSource{{LogicalName: "docs/canonical/cell-boundaries.yaml", Version: "0.1", Tier: TierImmutableSafety, InstructionClass: InstructionImmutableConstraint, TrustClass: TrustImmutable, DataClass: DataOrganizational, Content: []byte("safety"), ContentHash: DigestMarkdown([]byte("safety")), SemanticHash: DigestMarkdown([]byte("safety"))}}}}
	store := newMemoryStore()
	service, err := NewService(ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20}, reg, docs, canonical, NoopOwnerConstraintProvider{}, UnavailableMemoryProvider{}, emptySkillProvider{}, UnavailableProjectProvider{}, UnavailableTaskProvider{}, UnavailableRAGProvider{}, NewAssembler(), NewRenderer(), store, fixedClock{time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{service: service, registry: reg, docs: docs, store: store, canonical: canonical}
}
func (f *serviceFixture) request(key string) BuildRequest {
	return BuildRequest{OrganizationID: "explorarte", OrganizationRevisionID: 1, ActorRoleID: f.registry.role.ID, Purpose: "unit test", IdempotencyKey: key}
}

func agentDoc(path string) LoadedDocument {
	body := []byte("# Agent\n")
	return LoadedDocument{Path: path, Normalized: body, Body: body, Hash: DigestMarkdown(body)}
}
func profileDoc(path, department, role, memory string) LoadedDocument {
	body := []byte("# Profile\n")
	normalized := []byte("profile:" + department + "/" + role)
	return LoadedDocument{Path: path, Normalized: normalized, Body: body, Hash: DigestMarkdown(normalized), Frontmatter: map[string]any{"departamento": department, "rol": role, "dominio_memoria": memory, "agente_base": true}}
}
func pointer(value string) *string { return &value }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeRegistry struct {
	organization registry.Organization
	revision     *registry.Revision
	unit         registry.Unit
	role         registry.Role
}

func (f *fakeRegistry) GetOrganization(ctx context.Context, id string) (registry.Organization, error) {
	if err := ctx.Err(); err != nil {
		return registry.Organization{}, err
	}
	if id != f.organization.ID {
		return registry.Organization{}, registry.ErrNotFound
	}
	return f.organization, nil
}
func (f *fakeRegistry) GetUnit(ctx context.Context, org, id string) (registry.Unit, error) {
	if err := ctx.Err(); err != nil {
		return registry.Unit{}, err
	}
	if org != f.organization.ID || id != f.unit.ID {
		return registry.Unit{}, registry.ErrNotFound
	}
	return f.unit, nil
}
func (f *fakeRegistry) GetRole(ctx context.Context, org, id string) (registry.Role, error) {
	if err := ctx.Err(); err != nil {
		return registry.Role{}, err
	}
	if org != f.organization.ID || id != f.role.ID {
		return registry.Role{}, registry.ErrNotFound
	}
	return f.role, nil
}
func (f *fakeRegistry) GetCurrentRevision(ctx context.Context, org string) (*registry.Revision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if org != f.organization.ID {
		return nil, registry.ErrNotFound
	}
	copy := *f.revision
	return &copy, nil
}

type fakeDocuments struct{ docs map[string]LoadedDocument }

func (f *fakeDocuments) Load(ctx context.Context, path string, _ int64) (LoadedDocument, error) {
	if err := ctx.Err(); err != nil {
		return LoadedDocument{}, err
	}
	v, ok := f.docs[path]
	if !ok {
		return LoadedDocument{}, Reject(ReasonSourceNotFound, path, "not found")
	}
	return v, nil
}

type fakeCanonical struct{ bundle CanonicalBundle }

func (f *fakeCanonical) Load(ctx context.Context) (CanonicalBundle, error) {
	if err := ctx.Err(); err != nil {
		return CanonicalBundle{}, err
	}
	return f.bundle, nil
}
func (f *fakeCanonical) Validate(ctx context.Context, p, b string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p != f.bundle.PrecedenceHash {
		return Reject(ReasonPrecedenceHashMismatch, "p", "changed")
	}
	if b != f.bundle.BundleHash {
		return Reject(ReasonCanonicalBundleDrift, "b", "changed")
	}
	return nil
}

type emptySkillProvider struct{}

func (emptySkillProvider) ListActiveForRole(context.Context, string, string) ([]SkillRecord, error) {
	return nil, nil
}
func (emptySkillProvider) GetActiveForRole(context.Context, string, string, string) (SkillRecord, error) {
	return SkillRecord{}, Reject(ReasonSkillNotFound, "skill", "not found")
}
func (emptySkillProvider) ValidateVersion(context.Context, SkillRecord) error { return nil }

type memoryStore struct {
	mu    sync.Mutex
	next  int64
	byID  map[int64]Snapshot
	byKey map[string]int64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{next: 1, byID: map[int64]Snapshot{}, byKey: map[string]int64{}}
}
func (s *memoryStore) AllocateID(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	return id, nil
}
func (s *memoryStore) Create(_ context.Context, c CreateSnapshotCommand) (BuildResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := c.Snapshot.OrganizationID + "\x00" + c.Snapshot.IdempotencyKey
	if id, ok := s.byKey[key]; ok {
		v := s.byID[id]
		if v.RequestHash != c.Snapshot.RequestHash {
			return BuildResult{}, ErrIdempotencyConflict
		}
		return BuildResult{Snapshot: cloneSnapshot(v), Reused: true}, nil
	}
	s.byKey[key] = c.Snapshot.ID
	s.byID[c.Snapshot.ID] = cloneSnapshot(c.Snapshot)
	return BuildResult{Snapshot: cloneSnapshot(c.Snapshot)}, nil
}
func (s *memoryStore) Get(_ context.Context, id int64, _ bool) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[id]
	if !ok {
		return Snapshot{}, ErrSnapshotNotFound
	}
	return cloneSnapshot(v), nil
}
func (s *memoryStore) GetByIdempotency(_ context.Context, org, key string, _ bool) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byKey[org+"\x00"+key]
	if !ok {
		return Snapshot{}, ErrSnapshotNotFound
	}
	return cloneSnapshot(s.byID[id]), nil
}
func (s *memoryStore) List(context.Context, ListFilter) ([]Snapshot, error) { return nil, nil }
func (s *memoryStore) Invalidate(_ context.Context, c InvalidateCommand, now time.Time) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[c.SnapshotID]
	if !ok {
		return Snapshot{}, false, ErrSnapshotNotFound
	}
	if v.Status == SnapshotInvalidated {
		if v.InvalidationReason == c.Reason {
			return cloneSnapshot(v), true, nil
		}
		return Snapshot{}, false, ErrSnapshotInvalidated
	}
	v.Status = SnapshotInvalidated
	v.Version++
	v.InvalidatedAt = &now
	v.InvalidationReason = c.Reason
	s.byID[v.ID] = v
	return cloneSnapshot(v), false, nil
}
func (s *memoryStore) BindSelectorFacts(_ context.Context, snapshotID int64, taskClass, executionPurpose, actorUnitID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[snapshotID]
	if !ok {
		return Snapshot{}, ErrSnapshotNotFound
	}
	if v.TaskClass == "" && taskClass != "" {
		v.TaskClass = taskClass
	}
	if v.ExecutionPurpose == "" && executionPurpose != "" {
		v.ExecutionPurpose = executionPurpose
	}
	if v.ActorUnitID == "" && actorUnitID != "" {
		v.ActorUnitID = actorUnitID
	}
	s.byID[v.ID] = v
	return cloneSnapshot(v), nil
}

func cloneSnapshot(v Snapshot) Snapshot {
	v.Segments = append([]Segment(nil), v.Segments...)
	for i := range v.Segments {
		v.Segments[i].Content = append([]byte(nil), v.Segments[i].Content...)
	}
	return v
}
