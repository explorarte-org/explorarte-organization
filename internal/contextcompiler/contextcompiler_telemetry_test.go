package contextcompiler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestEstimateTokens_Boundaries is M1.2 section 21.A: exact deterministic
// values at every boundary of the frozen v1 formula, ceil(bytes/3).
func TestEstimateTokens_Boundaries(t *testing.T) {
	cases := []struct {
		bytes int
		want  int64
	}{
		{0, 0}, {1, 1}, {2, 1}, {3, 1}, {4, 2}, {5, 2}, {6, 2},
		{7, 3}, {9, 3}, {10, 4},
		{1_000_000, 333334},
	}
	for _, c := range cases {
		if got := EstimateTokens(c.bytes); got != c.want {
			t.Errorf("EstimateTokens(%d) = %d, want %d", c.bytes, got, c.want)
		}
	}
	if got := EstimateTokens(-5); got != 0 {
		t.Errorf("EstimateTokens(-5) = %d, want 0 (a negative byte count is never meaningful)", got)
	}
}

// TestBuildContextTokenTelemetry_Determinism is section 21.B.
func TestBuildContextTokenTelemetry_Determinism(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(300, "investigacion/research_worker_hourly", roleCatalogYAML())
	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildContextTokenTelemetry(view, snap)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildContextTokenTelemetry(view, snap)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("identical view+snapshot produced diverging telemetry:\n%s\nvs\n%s", firstJSON, secondJSON)
	}
}

// TestBuildContextTokenTelemetry_ProjectedResearch is section 21.C.
func TestBuildContextTokenTelemetry_ProjectedResearch(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(301, "investigacion/research_worker_hourly", roleCatalogYAML())
	before := orgSnapshot(301, "investigacion/research_worker_hourly", roleCatalogYAML())

	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if view.FellBackToCanonical {
		t.Fatal("research.corpus_curate/v1 must not fall back")
	}
	telemetry, err := BuildContextTokenTelemetry(view, snap)
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.ExecutionContextViewID != view.ID {
		t.Fatalf("telemetry does not reference the durable view: %d != %d", telemetry.ExecutionContextViewID, view.ID)
	}
	if telemetry.EstimatorID != EstimatorID || telemetry.EstimatorVersion != EstimatorVersion {
		t.Fatalf("telemetry missing estimator provenance: %+v", telemetry)
	}
	if len(telemetry.SegmentTokenEstimates) == 0 {
		t.Fatal("no segment estimates produced")
	}
	var sawProjected bool
	for _, seg := range telemetry.SegmentTokenEstimates {
		if seg.OriginalEstimatedTokens != EstimateTokens(seg.OriginalBytes) || seg.DeliveredEstimatedTokens != EstimateTokens(seg.DeliveredBytes) {
			t.Fatalf("segment estimate not derived from its own byte counts: %+v", seg)
		}
		if seg.Projected {
			sawProjected = true
			if seg.DeliveredBytes >= seg.OriginalBytes {
				t.Fatalf("projected segment did not shrink: %+v", seg)
			}
			// A projected segment must not gain authority: its tier is
			// still exactly the canonical tier, never elevated because it
			// became cheaper.
			for _, canonicalSeg := range snap.Segments {
				if canonicalSeg.SourceReference == seg.SourceReference {
					if canonicalSeg.AuthorityTier != seg.AuthorityTier {
						t.Fatalf("projected segment authority tier diverged from canonical: %+v", seg)
					}
				}
			}
		}
	}
	if !sawProjected {
		t.Fatal("research.corpus_curate/v1 fixture must exercise at least one projected segment")
	}
	// No canonical content mutation.
	if len(snap.Segments) != len(before.Segments) {
		t.Fatal("segment count changed by telemetry generation")
	}
	for i := range snap.Segments {
		if string(snap.Segments[i].Content) != string(before.Segments[i].Content) {
			t.Fatalf("segment %d content mutated by telemetry generation", i)
		}
	}
}

// TestBuildContextTokenTelemetry_GenericFallback is section 21.D.
func TestBuildContextTokenTelemetry_GenericFallback(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(302, "empresa/ceo", roleCatalogYAML())

	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if !view.FellBackToCanonical {
		t.Fatal("empresa/ceo must fall back to canonical")
	}
	telemetry, err := BuildContextTokenTelemetry(view, snap)
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.EstimatedProviderVisibleTokens == 0 {
		t.Fatal("fallback view must still produce a token estimate")
	}
	if len(telemetry.SegmentTokenEstimates) == 0 {
		t.Fatal("fallback must still produce segment estimates for every included canonical segment")
	}
	for _, seg := range telemetry.SegmentTokenEstimates {
		if seg.Projected {
			t.Fatalf("generic canonical fallback must never mark a segment as projected: %+v", seg)
		}
		if seg.DeliveredBytes != seg.OriginalBytes {
			t.Fatalf("generic fallback delivered bytes must equal original bytes: %+v", seg)
		}
	}
	var includedCanonical int
	for _, seg := range snap.Segments {
		if seg.Included {
			includedCanonical++
		}
	}
	if len(telemetry.SegmentTokenEstimates) != includedCanonical {
		t.Fatalf("fallback segment estimate count = %d, want %d included canonical segments", len(telemetry.SegmentTokenEstimates), includedCanonical)
	}
}

// TestBuildContextTokenTelemetry_ExactViewTotal is section 21.E: the total
// must derive from the durable ProviderVisibleByteCount, never a
// reconstructed current render.
func TestBuildContextTokenTelemetry_ExactViewTotal(t *testing.T) {
	snap := orgSnapshot(303, "empresa/ceo", roleCatalogYAML())
	bytes := []byte("some exact durable provider-visible payload")
	view := ExecutionContextView{
		ContextSnapshotID: snap.ID, OrganizationID: "explorarte",
		FellBackToCanonical: true, FallbackReason: "task_class_not_projected",
		AuthorityOrderHash: "a", CompiledContentHash: "b",
		ProviderVisibleBytes: bytes, ProviderVisibleDigest: sha256Hex(bytes), ProviderVisibleByteCount: len(bytes),
		StablePrefixBytes: 30, DynamicSuffixBytes: 12,
	}
	telemetry, err := BuildContextTokenTelemetry(view, snap)
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.EstimatedProviderVisibleTokens != EstimateTokens(len(bytes)) {
		t.Fatalf("total tokens = %d, want %d", telemetry.EstimatedProviderVisibleTokens, EstimateTokens(len(bytes)))
	}
	if telemetry.ProviderVisibleBytes != len(bytes) {
		t.Fatalf("provider visible bytes = %d, want %d", telemetry.ProviderVisibleBytes, len(bytes))
	}
	// TestBuildContextTokenTelemetry_StableDynamic is section 21.F, folded
	// in here since it uses the same fixture.
	if telemetry.EstimatedStablePrefixTokens != EstimateTokens(30) {
		t.Fatalf("stable prefix tokens = %d, want %d", telemetry.EstimatedStablePrefixTokens, EstimateTokens(30))
	}
	if telemetry.EstimatedDynamicSuffixTokens != EstimateTokens(12) {
		t.Fatalf("dynamic suffix tokens = %d, want %d", telemetry.EstimatedDynamicSuffixTokens, EstimateTokens(12))
	}
}

// TestBuildContextTokenTelemetry_NoSegmentContent is section 21.G: proves
// telemetry never carries segment Content, only metadata/counts/estimates.
func TestBuildContextTokenTelemetry_NoSegmentContent(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	const secretMarker = "TELEMETRY_MUST_NEVER_CONTAIN_THIS_RAW_CONTENT_MARKER"
	snap := orgSnapshot(304, "investigacion/research_worker_hourly", roleCatalogYAML())
	var mutated bool
	for i := range snap.Segments {
		// role-catalog.yaml's content is re-parsed as YAML by its own
		// projection function -- mutate only a segment that isn't, so this
		// test proves telemetry never leaks segment content without also
		// breaking an unrelated projection.
		if snap.Segments[i].Included && snap.Segments[i].SourceReference != "docs/canonical/role-catalog.yaml" {
			snap.Segments[i].Content = append(snap.Segments[i].Content, []byte(" "+secretMarker)...)
			snap.Segments[i].ByteCount = len(snap.Segments[i].Content)
			mutated = true
		}
	}
	if !mutated {
		t.Skip("fixture has no included non-role-catalog segment to mutate")
	}
	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	telemetry, err := BuildContextTokenTelemetry(view, snap)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(telemetry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secretMarker) {
		t.Fatalf("telemetry leaked raw segment content: %s", body)
	}
}

// TestBuildContextTokenTelemetry_WrongSnapshot is section 21.H.
func TestBuildContextTokenTelemetry_WrongSnapshot(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(305, "empresa/ceo", roleCatalogYAML())
	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	wrongSnapshot := orgSnapshot(306, "empresa/ceo", roleCatalogYAML())
	if _, err := BuildContextTokenTelemetry(view, wrongSnapshot); !errors.Is(err, ErrContextTokenTelemetryBinding) {
		t.Fatalf("want ErrContextTokenTelemetryBinding, got %v", err)
	}
}

// TestBuildContextTokenTelemetry_RejectsCorruptView is section 21.I: a view
// that fails M1.1's own ValidateIntegrity must never produce trusted token
// telemetry.
func TestBuildContextTokenTelemetry_RejectsCorruptView(t *testing.T) {
	snap := orgSnapshot(307, "empresa/ceo", roleCatalogYAML())
	view := ExecutionContextView{
		ContextSnapshotID: snap.ID, OrganizationID: "explorarte",
		FellBackToCanonical: true, FallbackReason: "task_class_not_projected",
		AuthorityOrderHash: "a", CompiledContentHash: "b",
		ProviderVisibleBytes: []byte("hello"), ProviderVisibleDigest: sha256Hex([]byte("hello")),
		ProviderVisibleByteCount: 999, // corrupt on purpose
	}
	if _, err := BuildContextTokenTelemetry(view, snap); !errors.Is(err, ErrExecutionContextViewIntegrity) {
		t.Fatalf("want ErrExecutionContextViewIntegrity, got %v", err)
	}
}

// TestBuildContextTokenTelemetry_MisattributedSegmentDiffsFailClosed proves
// that if SegmentDiffs cannot be safely positionally attributed to
// canonical.Segments, telemetry generation fails closed instead of
// silently mapping a diff to the wrong segment.
func TestBuildContextTokenTelemetry_MisattributedSegmentDiffsFailClosed(t *testing.T) {
	snap := orgSnapshot(308, "investigacion/research_worker_hourly", roleCatalogYAML())
	bytes := []byte("provider visible bytes")
	view := ExecutionContextView{
		ContextSnapshotID: snap.ID, OrganizationID: "explorarte",
		ContextProfileID: ResearchCorpusCurateV1TaskClass, ContextProfileVersion: "v1",
		FellBackToCanonical: false, ProviderRenderVersion: "v1",
		AuthorityOrderHash: "a", CompiledContentHash: "b",
		SegmentDiffs:         []SegmentDiff{{SourceReference: "not-a-real-source-reference", OriginalBytes: 5, ProjectedBytes: 5}},
		ProviderVisibleBytes: bytes, ProviderVisibleDigest: sha256Hex(bytes), ProviderVisibleByteCount: len(bytes),
	}
	if len(snap.Segments) == 1 {
		t.Skip("fixture needs more than one segment to prove misattribution detection meaningfully")
	}
	if _, err := BuildContextTokenTelemetry(view, snap); !errors.Is(err, ErrContextTokenTelemetryAttribution) {
		t.Fatalf("want ErrContextTokenTelemetryAttribution, got %v", err)
	}
}
