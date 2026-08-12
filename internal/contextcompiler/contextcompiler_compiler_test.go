package contextcompiler

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func roleCatalogYAML() []byte {
	return []byte(`schema_version: 0.1.0
document_status: branch_0_candidate
roles:
- id: empresa/ceo
  department: empresa
  summary: CEO summary text that is irrelevant to corpus curation.
- id: investigacion/research_worker_hourly
  department: investigacion
  summary: Research worker contract.
- id: otro/rol
  department: otro
  summary: Another unrelated role.
`)
}

func testSnapshot(actorRoleID string, roleCatalogContent []byte) contextengine.Snapshot {
	segments := []contextengine.Segment{
		{Ordinal: 1, RenderOrdinal: 1, AuthorityPriority: 0, AuthorityTier: contextengine.TierImmutableSafety, SourceReference: "docs/canonical/cell-boundaries.yaml", Included: true, Content: []byte("safety"), ByteCount: 6, ContentHash: "h1"},
		{Ordinal: 2, RenderOrdinal: 2, AuthorityPriority: 1, AuthorityTier: contextengine.TierOwnerDecisions, SourceReference: "docs/canonical/decisions-required.yaml", Included: true, Content: []byte("owner"), ByteCount: 5, ContentHash: "h2"},
		{Ordinal: 3, RenderOrdinal: 3, AuthorityPriority: 2, AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: RoleCatalogSourceReference, Included: true, Content: roleCatalogContent, ByteCount: len(roleCatalogContent), ContentHash: "h3"},
		{Ordinal: 4, RenderOrdinal: 4, AuthorityPriority: 3, AuthorityTier: contextengine.TierOrganizationAgent, SourceReference: "AGENT.md", Included: true, Content: []byte("org"), ByteCount: 3, ContentHash: "h4"},
		{Ordinal: 5, RenderOrdinal: 5, AuthorityPriority: 3, AuthorityTier: contextengine.TierDepartmentAgent, SourceReference: "investigacion/AGENT.md", Included: true, Content: []byte("dept"), ByteCount: 4, ContentHash: "h5"},
		{Ordinal: 6, RenderOrdinal: 6, AuthorityPriority: 4, AuthorityTier: contextengine.TierRoleProfile, SourceReference: "investigacion/research_worker_hourly/PERFIL.md", Included: true, Content: []byte("perfil"), ByteCount: 6, ContentHash: "h6"},
		{Ordinal: 7, RenderOrdinal: 7, AuthorityPriority: 5, AuthorityTier: contextengine.TierTask, SourceReference: "task:1", Included: true, Content: []byte("task payload"), ByteCount: 12, ContentHash: "h7"},
	}
	return contextengine.Snapshot{ID: 1, ActorRoleID: actorRoleID, Segments: segments}
}

func TestCompile_RoleCatalogProjectedToSelfEntry(t *testing.T) {
	profile := ResearchCorpusCurateV1()
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	result, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("expected a real projection, not a fallback")
	}
	var roleCatalogSeg contextengine.Segment
	for _, seg := range result.Projected.Segments {
		if seg.SourceReference == RoleCatalogSourceReference {
			roleCatalogSeg = seg
		}
	}
	if roleCatalogSeg.ByteCount >= len(roleCatalogYAML()) {
		t.Fatalf("expected role-catalog.yaml projection to shrink bytes, got %d (original %d)", roleCatalogSeg.ByteCount, len(roleCatalogYAML()))
	}
	if !containsString(string(roleCatalogSeg.Content), "research_worker_hourly") {
		t.Fatal("projected role-catalog content must still contain the actor's own entry")
	}
	if containsString(string(roleCatalogSeg.Content), "empresa/ceo") || containsString(string(roleCatalogSeg.Content), "otro/rol") {
		t.Fatal("projected role-catalog content must NOT contain other roles' entries")
	}
}

func TestCompile_RequiredTierMissingFallsBackToCanonical(t *testing.T) {
	profile := ResearchCorpusCurateV1()
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	// Drop the immutable_safety segment entirely -- a required tier.
	snap.Segments = snap.Segments[1:]
	result, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !result.FellBackToCanonical {
		t.Fatal("expected fallback to canonical when a required tier is missing, never a partial view")
	}
}

func TestCompileForTaskClass_UnknownActorFallsBackNeverMinimal(t *testing.T) {
	snap := testSnapshot("empresa/ceo", roleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if !result.FellBackToCanonical {
		t.Fatal("expected fallback to canonical for an actor with no registered profile")
	}
	if len(result.Projected.Segments) != len(snap.Segments) {
		t.Fatalf("fallback must return every canonical segment unchanged, got %d want %d", len(result.Projected.Segments), len(snap.Segments))
	}
}

func TestCompile_Determinism(t *testing.T) {
	profile := ResearchCorpusCurateV1()
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	r1, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	r2, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if r1.CompiledContentHash != r2.CompiledContentHash {
		t.Fatalf("Compile must be deterministic: %s != %s", r1.CompiledContentHash, r2.CompiledContentHash)
	}
	if r1.AuthorityOrderHash != r2.AuthorityOrderHash {
		t.Fatal("AuthorityOrderHash must be deterministic")
	}
}

func TestCompile_AuthorityOrderHashStableAcrossDifferentTaskPayloads(t *testing.T) {
	// Section 48: stable prefix must produce the SAME hash across two
	// DIFFERENT clusters -- only task_context (dynamic) should ever
	// differ between two real invocations for the same actor.
	profile := ResearchCorpusCurateV1()
	snapA := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	snapB := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	for i := range snapB.Segments {
		if snapB.Segments[i].AuthorityTier == contextengine.TierTask {
			snapB.Segments[i].Content = []byte("a completely different cluster payload")
			snapB.Segments[i].ByteCount = len(snapB.Segments[i].Content)
			snapB.Segments[i].ContentHash = "different-hash"
		}
	}
	resultA, err := Compile(profile, snapA)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	resultB, err := Compile(profile, snapB)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if resultA.AuthorityOrderHash != resultB.AuthorityOrderHash {
		t.Fatal("AuthorityOrderHash must be identical across two different clusters for the same actor/profile")
	}
	if resultA.CompiledContentHash == resultB.CompiledContentHash {
		t.Fatal("CompiledContentHash must differ when the dynamic task payload differs")
	}
	if resultA.StablePrefixBytes != resultB.StablePrefixBytes {
		t.Fatal("StablePrefixBytes must be identical when only the dynamic suffix changes")
	}
}

func TestRoleCatalogSelfEntry_ActorNotFoundFallsBackToFullCatalog(t *testing.T) {
	seg := contextengine.Segment{Content: roleCatalogYAML()}
	content, reason, err := RoleCatalogSelfEntry(seg, "nonexistent/role")
	if err != nil {
		t.Fatalf("RoleCatalogSelfEntry: %v", err)
	}
	if len(content) != 0 {
		t.Fatal("expected no projected content when the actor's own entry cannot be found")
	}
	if reason == "" {
		t.Fatal("expected a non-empty fallback reason")
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
