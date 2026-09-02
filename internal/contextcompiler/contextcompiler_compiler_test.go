package contextcompiler

import (
	"bytes"
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
	snap := contextengine.Snapshot{ID: 1, ActorRoleID: actorRoleID, Segments: segments}
	// M1.3: TaskClass/ActorUnitID are durable selector facts a real
	// research task would already carry (host-assigned/propagated by
	// Executive, never inferred here) -- set them exactly as a real
	// research.corpus_curate execution for these two roles would, so
	// every existing fixture built through this helper keeps exercising
	// the SAME positive-path scenario it always did. A non-research
	// actorRoleID gets neither, and correctly canonical-falls-back.
	if actorRoleID == researchWorkerHourlyRoleID {
		snap.TaskClass = ResearchCorpusCurateV1TaskClass
		snap.ActorUnitID = researchUnitID
	}
	return snap
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

// R31 hardening §18.3: contextcompiler's telemetry must use the exact same
// stable/dynamic partition rule as contextengine.BuildProviderRender
// (ProviderRender v1), not an independently-maintained, narrower one.
// Before this fix, this telemetry only ever treated TierTask as dynamic --
// a TierRAGEvidence segment would have been silently counted as part of
// StablePrefixBytes here, while ProviderRender itself already routed it to
// DynamicSuffix. This test adds a TierRAGEvidence segment and asserts the
// compiler's byte totals agree with that partition.
func TestCompile_TelemetryUsesSameDynamicPartitionAsProviderRender(t *testing.T) {
	profile := ResearchCorpusCurateV1()
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	ragSegment := contextengine.Segment{
		Ordinal: 8, RenderOrdinal: 8, AuthorityPriority: 5, AuthorityTier: contextengine.TierRAGEvidence,
		SourceReference: "rag:evidence-1", Included: true, Content: []byte("untrusted rag evidence content"), ByteCount: 31, ContentHash: "h8",
	}
	snap.Segments = append(snap.Segments, ragSegment)

	result, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !contextengine.IsDynamicProviderTier(contextengine.TierRAGEvidence) {
		t.Fatal("sanity check failed: TierRAGEvidence must be dynamic per IsDynamicProviderTier")
	}
	// DynamicSuffixBytes must include BOTH the original task_context
	// segment (12 bytes) AND the new RAG evidence segment (31 bytes) = 43.
	// Before the fix, DynamicSuffixBytes would have been only 12 (the RAG
	// segment silently miscounted as stable).
	if result.DynamicSuffixBytes != 12+31 {
		t.Fatalf("DynamicSuffixBytes=%d, want 43 (task_context 12 + rag_evidence 31) -- RAG evidence must be counted as dynamic, matching ProviderRender", result.DynamicSuffixBytes)
	}
}

// TestCompile_PartitionBytesMatchProviderRenderBytePartition checks that the
// compiler's stable/dynamic partition still agrees with the one ProviderRender
// actually produces.
//
// It previously asserted exact byte equality on the dynamic side. That worked
// only while ProviderRender emitted segment content verbatim: with one dynamic
// segment and no separators, rendered bytes happened to equal the raw
// ByteCount sum. Security hardening v1 made BuildProviderRender delegate to
// BuildProviderRenderV2, which wraps untrusted content in explicit
// authority/trust markers and escapes it, so the rendered dynamic side is now
// deliberately larger than the raw content it carries. Exact equality can no
// longer hold without giving up the wrapping, and the wrapping is the whole
// point of v2.
//
// So the assertions below test the invariant the original comment already
// named as the one that MUST hold -- a segment contributes to the same SIDE in
// both -- plus two properties that equality alone never checked: that the
// divergence is bounded in the only direction wrapping can push it, and that
// it is actually caused by the security wrapper rather than by a partition
// bug wearing its costume.
func TestCompile_PartitionBytesMatchProviderRenderBytePartition(t *testing.T) {
	profile := ResearchCorpusCurateV1()
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())

	result, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	render, err := contextengine.BuildProviderRender(result.Projected)
	if err != nil {
		t.Fatalf("BuildProviderRender: %v", err)
	}

	// Side agreement: a side is empty in the compiler's accounting exactly
	// when it is empty in the render. This is what catches the partition rule
	// diverging again -- a segment silently moving from one side to the other
	// shows up here, wrapping or no wrapping.
	if (result.DynamicSuffixBytes == 0) != (render.DynamicSuffixBytes == 0) {
		t.Fatalf("dynamic side disagrees on emptiness: compiler=%d render=%d -- the partition rule has diverged",
			result.DynamicSuffixBytes, render.DynamicSuffixBytes)
	}
	if (result.StablePrefixBytes == 0) != (render.StablePrefixBytes == 0) {
		t.Fatalf("stable side disagrees on emptiness: compiler=%d render=%d -- the partition rule has diverged",
			result.StablePrefixBytes, render.StablePrefixBytes)
	}

	// Wrapping only ever adds bytes. If the render carries fewer bytes than
	// the raw content the compiler counted, content was dropped somewhere,
	// which no amount of escaping explains.
	if render.DynamicSuffixBytes < result.DynamicSuffixBytes {
		t.Fatalf("render dynamic side (%d bytes) is smaller than the raw content it must carry (%d bytes) -- content was lost",
			render.DynamicSuffixBytes, result.DynamicSuffixBytes)
	}
	if render.StablePrefixBytes < result.StablePrefixBytes {
		t.Fatalf("render stable side (%d bytes) is smaller than the raw content it must carry (%d bytes) -- content was lost",
			render.StablePrefixBytes, result.StablePrefixBytes)
	}

	// The excess must be the v2 authority wrapper. Asserting this keeps the
	// test from silently tolerating an unexplained size difference: if
	// BuildProviderRender ever stopped wrapping (falling back to v1, say),
	// the sizes would converge and this check would fail -- which is the
	// correct outcome, because losing the wrapper is a security regression,
	// not a cosmetic one.
	if result.DynamicSuffixBytes > 0 {
		if !bytes.Contains(render.DynamicSuffix, []byte("authority")) {
			t.Fatalf("dynamic suffix carries content but no v2 authority marker: untrusted data is being rendered unwrapped")
		}
		if render.DynamicSuffixBytes == result.DynamicSuffixBytes {
			t.Fatalf("dynamic side rendered byte-for-byte identical to raw content (%d bytes): the v2 wrapper is not being applied",
				render.DynamicSuffixBytes)
		}
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
