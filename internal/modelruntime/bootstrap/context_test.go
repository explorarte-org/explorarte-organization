package bootstrap

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	execruntimeadapter "github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type fakeCtxService struct {
	snapshot contextengine.Snapshot
}

func (f *fakeCtxService) Build(_ context.Context, request contextengine.BuildRequest) (contextengine.BuildResult, error) {
	snap := f.snapshot
	snap.ActorRoleID = request.ActorRoleID
	return contextengine.BuildResult{Snapshot: snap}, nil
}
func (f *fakeCtxService) Get(context.Context, int64, bool) (contextengine.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeCtxService) List(context.Context, contextengine.ListFilter) ([]contextengine.Snapshot, error) {
	return nil, nil
}
func (f *fakeCtxService) Render(ctx context.Context, _ int64) ([]byte, error) {
	return contextengine.NewRenderer().Render(ctx, f.snapshot)
}
func (f *fakeCtxService) Validate(context.Context, int64) (contextengine.SnapshotValidation, error) {
	return contextengine.SnapshotValidation{Valid: true}, nil
}
func (f *fakeCtxService) Invalidate(context.Context, contextengine.InvalidateCommand) (contextengine.Snapshot, error) {
	return contextengine.Snapshot{}, nil
}

func ctxFixtureSnapshot(actorRoleID string) contextengine.Snapshot {
	roleCatalog := []byte(`schema_version: 0.1.0
document_status: branch_0_candidate
roles:
- id: empresa/ceo
  department: empresa
  summary: CEO summary text that is irrelevant to corpus curation.
- id: investigacion/research_worker_hourly
  department: investigacion
  summary: Research worker contract.
`)
	segments := []contextengine.Segment{
		{Ordinal: 1, RenderOrdinal: 1, AuthorityPriority: 0, AuthorityTier: contextengine.TierImmutableSafety, SourceReference: "docs/canonical/cell-boundaries.yaml", Included: true, Content: []byte("safety"), ByteCount: 6, ContentHash: "h1"},
		{Ordinal: 2, RenderOrdinal: 2, AuthorityPriority: 1, AuthorityTier: contextengine.TierOwnerDecisions, SourceReference: "docs/canonical/decisions-required.yaml", Included: true, Content: []byte("owner"), ByteCount: 5, ContentHash: "h2"},
		{Ordinal: 3, RenderOrdinal: 3, AuthorityPriority: 2, AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: contextcompiler.RoleCatalogSourceReference, Included: true, Content: roleCatalog, ByteCount: len(roleCatalog), ContentHash: "h3"},
		{Ordinal: 4, RenderOrdinal: 4, AuthorityPriority: 3, AuthorityTier: contextengine.TierOrganizationAgent, SourceReference: "AGENT.md", Included: true, Content: []byte("org"), ByteCount: 3, ContentHash: "h4"},
		{Ordinal: 5, RenderOrdinal: 5, AuthorityPriority: 3, AuthorityTier: contextengine.TierDepartmentAgent, SourceReference: "investigacion/AGENT.md", Included: true, Content: []byte("dept"), ByteCount: 4, ContentHash: "h5"},
		{Ordinal: 6, RenderOrdinal: 6, AuthorityPriority: 4, AuthorityTier: contextengine.TierRoleProfile, SourceReference: actorRoleID + "/PERFIL.md", Included: true, Content: []byte("perfil"), ByteCount: 6, ContentHash: "h6"},
		{Ordinal: 7, RenderOrdinal: 7, AuthorityPriority: 5, AuthorityTier: contextengine.TierTask, SourceReference: "task:1", Included: true, Content: []byte("task payload"), ByteCount: 12, ContentHash: "h7"},
	}
	snap := contextengine.Snapshot{ID: 1, Version: 1, Status: contextengine.SnapshotReady, OrganizationID: "explorarte", ActorRoleID: actorRoleID, Segments: segments}
	// M1.3: the durable selector facts a real research task would carry
	// (never inferred from ActorRoleID alone anymore).
	if actorRoleID == "investigacion/research_worker_hourly" {
		snap.TaskClass = contextcompiler.ResearchCorpusCurateV1TaskClass
		snap.ActorUnitID = "investigacion"
	}
	return snap
}

// TestContextAdapter_GenericFallbackAndProjectedResearch proves Model
// Runtime's real contextAdapter (RenderContextSnapshot / GetContextSnapshot)
// -- not a reimplementation -- produces exactly what the shared
// contextcompiler.ResolveProviderContext resolver produces for the same
// canonical snapshot, mirroring
// internal/executive/runtimeadapter.TestContextBuild_GenericFallbackAndProjectedResearch,
// and that PrepareModelInput accepts the durably resolved render (section 9.H).
func TestContextAdapter_GenericFallbackAndProjectedResearch(t *testing.T) {
	cases := []struct {
		name         string
		actorRoleID  string
		wantFellBack bool
	}{
		{"generic_fallback", "empresa/ceo", true},
		{"projected_research", "investigacion/research_worker_hourly", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := ctxFixtureSnapshot(tc.actorRoleID)
			svc := &fakeCtxService{snapshot: snap}
			adapter := contextAdapter{service: svc, store: contextcompiler.NewMemoryStore()}

			gotBytes, err := adapter.RenderContextSnapshot(context.Background(), snap.ID)
			if err != nil {
				t.Fatal(err)
			}
			gotRef, err := adapter.GetContextSnapshot(context.Background(), snap.ID)
			if err != nil {
				t.Fatal(err)
			}

			want, err := contextcompiler.ResolveProviderContext(context.Background(), snap)
			if err != nil {
				t.Fatal(err)
			}
			if want.FellBack != tc.wantFellBack {
				t.Fatalf("test fixture bug: FellBack=%v want=%v", want.FellBack, tc.wantFellBack)
			}

			if string(gotBytes) != string(want.Bytes) {
				t.Fatalf("Model Runtime RenderContextSnapshot diverged from the shared resolver:\ngot=%s\nwant=%s", gotBytes, want.Bytes)
			}
			if gotRef.RenderedHash != want.Digest {
				t.Fatalf("Model Runtime GetContextSnapshot digest diverged from the shared resolver: got=%s want=%s", gotRef.RenderedHash, want.Digest)
			}

			prepared, err := modelruntime.PrepareModelInput(nil, gotRef, gotBytes)
			if err != nil {
				t.Fatalf("PrepareModelInput rejected the resolved render: %v", err)
			}
			if prepared.Envelope.StablePrefix[0].Content != string(gotBytes) {
				t.Fatal("prepared stable prefix does not match the resolved render")
			}
		})
	}
}

// TestExecutiveAndModelRuntimeShareTheSameDurableViewIdentity is section
// 9.G: for the same canonical snapshot, Executive's real Context.Build
// adapter (internal/executive/runtimeadapter) and Model Runtime's real
// contextAdapter, sharing the SAME ExecutionContextViewStore, resolve to the
// SAME durable ExecutionContextView ID and the SAME provider-visible
// digest -- not merely two independently reconstructed equal byte slices.
func TestExecutiveAndModelRuntimeShareTheSameDurableViewIdentity(t *testing.T) {
	for _, actorRoleID := range []string{"empresa/ceo", "investigacion/research_worker_hourly"} {
		t.Run(actorRoleID, func(t *testing.T) {
			snap := ctxFixtureSnapshot(actorRoleID)
			sharedStore := contextcompiler.NewMemoryStore()

			execAdapter := execruntimeadapter.Context{
				Service:        &fakeCtxService{snapshot: snap},
				Assembly:       contextcompiler.ContextAssemblyService{Store: sharedStore},
				OrganizationID: "explorarte",
			}
			execResult, err := execAdapter.Build(context.Background(), executive.ContextRequest{ActorRoleID: actorRoleID, Purpose: "department_worker"})
			if err != nil {
				t.Fatal(err)
			}

			modelRuntimeAdapter := contextAdapter{service: &fakeCtxService{snapshot: snap}, store: sharedStore}
			modelRuntimeBytes, err := modelRuntimeAdapter.RenderContextSnapshot(context.Background(), snap.ID)
			if err != nil {
				t.Fatal(err)
			}
			modelRuntimeRef, err := modelRuntimeAdapter.GetContextSnapshot(context.Background(), snap.ID)
			if err != nil {
				t.Fatal(err)
			}

			if string(modelRuntimeBytes) != execResult.Content {
				t.Fatalf("bytes diverged: executive=%s model_runtime=%s", execResult.Content, modelRuntimeBytes)
			}
			if modelRuntimeRef.RenderedHash != execResult.Digest {
				t.Fatalf("digest diverged: executive=%s model_runtime=%s", execResult.Digest, modelRuntimeRef.RenderedHash)
			}

			// The actual regression this mission closes: not just equal
			// bytes, but the SAME durable view row.
			modelRuntimeView, err := sharedStore.GetByContextSnapshot(context.Background(), "explorarte", snap.ID)
			if err != nil {
				t.Fatal(err)
			}
			if modelRuntimeView.ID != execResult.ExecutionContextViewID {
				t.Fatalf("Executive and Model Runtime durable view identities diverged: executive=%d shared_store=%d", execResult.ExecutionContextViewID, modelRuntimeView.ID)
			}

			prepared, err := modelruntime.PrepareModelInput(nil, modelRuntimeRef, modelRuntimeBytes)
			if err != nil {
				t.Fatalf("PrepareModelInput rejected the shared durable render: %v", err)
			}
			if prepared.Envelope.StablePrefix[0].Content != string(modelRuntimeBytes) {
				t.Fatal("prepared stable prefix does not match the shared durable render")
			}
		})
	}
}
