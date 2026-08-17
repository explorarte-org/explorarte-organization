package runtimeadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// fakeContextService is a minimal contextengine.Service double that returns
// a fixed, pre-built snapshot regardless of the build request, so this test
// can exercise Context.Build's real logic (including the call into the
// shared contextcompiler.ResolveProviderContext resolver) without a live
// Postgres-backed Context Engine.
type fakeContextService struct {
	snapshot contextengine.Snapshot
}

func (f *fakeContextService) Build(_ context.Context, request contextengine.BuildRequest) (contextengine.BuildResult, error) {
	snap := f.snapshot
	snap.ActorRoleID = request.ActorRoleID
	return contextengine.BuildResult{Snapshot: snap}, nil
}
func (f *fakeContextService) Get(_ context.Context, _ int64, _ bool) (contextengine.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeContextService) List(context.Context, contextengine.ListFilter) ([]contextengine.Snapshot, error) {
	return nil, nil
}
func (f *fakeContextService) Render(ctx context.Context, _ int64) ([]byte, error) {
	return contextengine.NewRenderer().Render(ctx, f.snapshot)
}
func (f *fakeContextService) Validate(context.Context, int64) (contextengine.SnapshotValidation, error) {
	return contextengine.SnapshotValidation{Valid: true}, nil
}
func (f *fakeContextService) Invalidate(context.Context, contextengine.InvalidateCommand) (contextengine.Snapshot, error) {
	return contextengine.Snapshot{}, nil
}

// fixtureSnapshot mirrors the segment composition contextcompiler's own
// tests use (research.corpus_curate/v1's real RequiredTiers), duplicated
// here as test data only -- the render/compile logic under test is not
// duplicated, both this test and contextcompiler's own tests call the same
// exported ResolveProviderContext.
func fixtureSnapshot(actorRoleID string) contextengine.Snapshot {
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
	return contextengine.Snapshot{ID: 1, Version: 1, Status: contextengine.SnapshotReady, ActorRoleID: actorRoleID, Segments: segments}
}

// TestContextBuild_GenericFallbackAndProjectedResearch proves Executive's
// real Context.Build adapter -- not a reimplementation of it -- produces
// exactly what the shared contextcompiler.ResolveProviderContext resolver
// produces for the same canonical snapshot, for both the generic-fallback
// case (no registered profile) and the projected research.corpus_curate/v1
// case. This is the "Executive-side" half of the byte/digest identity proof
// with Model Runtime's own adapter (internal/modelruntime/bootstrap).
func TestContextBuild_GenericFallbackAndProjectedResearch(t *testing.T) {
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
			snap := fixtureSnapshot(tc.actorRoleID)
			svc := &fakeContextService{snapshot: snap}
			adapter := Context{Service: svc, OrganizationID: "explorarte"}

			got, err := adapter.Build(context.Background(), executive.ContextRequest{ActorRoleID: tc.actorRoleID, Purpose: "department_worker"})
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

			if got.Content != string(want.Bytes) {
				t.Fatalf("Executive Content diverged from the shared resolver:\ngot=%s\nwant=%s", got.Content, want.Bytes)
			}
			if got.Digest != want.Digest {
				t.Fatalf("Executive Digest diverged from the shared resolver: got=%s want=%s", got.Digest, want.Digest)
			}
			sum := sha256.Sum256([]byte(got.Content))
			if got.Digest != hex.EncodeToString(sum[:]) {
				t.Fatalf("Executive Digest is not sha256(Content): digest=%s content_sha256=%x", got.Digest, sum)
			}
			if got.ID != snap.ID || got.Version != strconv.FormatInt(snap.Version, 10) {
				t.Fatalf("unexpected snapshot identity: %+v", got)
			}
		})
	}
}
