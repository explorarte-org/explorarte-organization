package contextcompiler

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func orgSnapshot(id int64, actorRoleID string, roleCatalogContent []byte) contextengine.Snapshot {
	snap := testSnapshot(actorRoleID, roleCatalogContent)
	snap.ID = id
	snap.OrganizationID = "explorarte"
	return snap
}

// TestResolveAndPersist_ProjectedResearchDurability is section 9.A: the
// projected research.corpus_curate/v1 view durably records everything the
// mission requires and the projected bytes differ from canonical.
func TestResolveAndPersist_ProjectedResearchDurability(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(1, "investigacion/research_worker_hourly", roleCatalogYAML())

	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if view.FellBackToCanonical {
		t.Fatal("projected research must not fall back")
	}
	if view.ContextSnapshotID != snap.ID {
		t.Fatalf("view does not reference the canonical snapshot: %d != %d", view.ContextSnapshotID, snap.ID)
	}
	if view.ContextProfileID != ResearchCorpusCurateV1TaskClass || view.ContextProfileVersion != "v1" {
		t.Fatalf("profile identity not persisted: id=%q version=%q", view.ContextProfileID, view.ContextProfileVersion)
	}
	if view.ProviderVisibleDigest == "" || len(view.ProviderVisibleBytes) == 0 {
		t.Fatal("provider-visible digest/bytes not persisted")
	}
	if len(view.SegmentDiffs) == 0 {
		t.Fatal("segment diffs did not survive persistence")
	}
	if view.StablePrefixBytes == 0 && view.DynamicSuffixBytes == 0 {
		t.Fatal("stable/dynamic byte partition did not survive persistence")
	}

	canonical, err := contextengine.NewRenderer().Render(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if string(view.ProviderVisibleBytes) == string(canonical) {
		t.Fatal("projected durable view must not equal the canonical render")
	}

	reloaded, err := store.GetByContextSnapshot(context.Background(), "explorarte", snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ID != view.ID || string(reloaded.ProviderVisibleBytes) != string(view.ProviderVisibleBytes) || len(reloaded.SegmentDiffs) != len(view.SegmentDiffs) {
		t.Fatalf("reloaded view diverged from persisted view: %+v vs %+v", reloaded, view)
	}
}

// TestResolveAndPersist_GenericFallbackDurability is section 9.B.
func TestResolveAndPersist_GenericFallbackDurability(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(2, "empresa/ceo", roleCatalogYAML())

	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if !view.FellBackToCanonical {
		t.Fatal("unregistered task class must fall back")
	}
	if view.FallbackReason == "" {
		t.Fatal("fallback reason not persisted")
	}
	canonical, err := contextengine.NewRenderer().Render(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if string(view.ProviderVisibleBytes) != string(canonical) {
		t.Fatal("generic fallback provider-visible bytes must equal canonical PortableRenderer bytes")
	}
	if view.ID == snap.ID {
		t.Fatal("durable view identity must be distinct from the canonical snapshot's own ID (coincidence in this fixture would be misleading either way, but they are different identity spaces)")
	}
	reloaded, err := store.Get(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.ProviderVisibleBytes) != string(view.ProviderVisibleBytes) || reloaded.ProviderVisibleDigest != view.ProviderVisibleDigest {
		t.Fatal("reload did not reproduce the exact persisted bytes/digest")
	}
}

// TestResolveAndPersist_Idempotent is section 9.C.
func TestResolveAndPersist_Idempotent(t *testing.T) {
	for _, actorRoleID := range []string{"empresa/ceo", "investigacion/research_worker_hourly"} {
		t.Run(actorRoleID, func(t *testing.T) {
			store := NewMemoryStore()
			svc := ContextAssemblyService{Store: store}
			snap := orgSnapshot(3, actorRoleID, roleCatalogYAML())

			first, err := svc.ResolveAndPersist(context.Background(), snap)
			if err != nil {
				t.Fatal(err)
			}
			second, err := svc.ResolveAndPersist(context.Background(), snap)
			if err != nil {
				t.Fatal(err)
			}
			if first.ID != second.ID {
				t.Fatalf("resolving the same snapshot twice created two durable views: %d != %d", first.ID, second.ID)
			}
			if string(first.ProviderVisibleBytes) != string(second.ProviderVisibleBytes) || first.ProviderVisibleDigest != second.ProviderVisibleDigest {
				t.Fatal("idempotent resolution produced different bytes/digest")
			}
		})
	}
}

// TestResolveAndPersist_DriftFailsClosed is section 9.D: persisting
// different content for a context_snapshot_id that already has a durable
// view must fail closed, never silently overwrite or silently accept.
func TestResolveAndPersist_DriftFailsClosed(t *testing.T) {
	store := NewMemoryStore()
	snap := orgSnapshot(4, "empresa/ceo", roleCatalogYAML())
	base := ExecutionContextView{
		OrganizationID: "explorarte", ContextSnapshotID: snap.ID,
		FellBackToCanonical: true, FallbackReason: "task_class_not_projected",
		AuthorityOrderHash: "a", CompiledContentHash: "b",
		ProviderVisibleBytes: []byte("original"), ProviderVisibleDigest: memSHA256Hex([]byte("original")), ProviderVisibleByteCount: len("original"),
	}
	if _, err := store.Persist(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	drifted := base
	drifted.ProviderVisibleBytes = []byte("different content")
	drifted.ProviderVisibleDigest = memSHA256Hex(drifted.ProviderVisibleBytes)
	drifted.ProviderVisibleByteCount = len(drifted.ProviderVisibleBytes)
	_, err := store.Persist(context.Background(), drifted)
	if !errors.Is(err, ErrExecutionContextViewDrift) {
		t.Fatalf("want ErrExecutionContextViewDrift, got %v", err)
	}
	// The original record must survive untouched.
	existing, err := store.GetByContextSnapshot(context.Background(), "explorarte", snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(existing.ProviderVisibleBytes) != "original" {
		t.Fatalf("drift attempt corrupted the existing durable view: %s", existing.ProviderVisibleBytes)
	}
}

// TestGet_RejectsTamperedIntegrity is section 9.E.
func TestGet_RejectsTamperedIntegrity(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(5, "empresa/ceo", roleCatalogYAML())
	view, err := svc.ResolveAndPersist(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	store.CorruptForTest(view.ID, []byte("tampered bytes"), "")
	if _, err := store.Get(context.Background(), view.ID); !errors.Is(err, ErrExecutionContextViewIntegrity) {
		t.Fatalf("want ErrExecutionContextViewIntegrity, got %v", err)
	}
	if _, err := store.GetByContextSnapshot(context.Background(), "explorarte", snap.ID); !errors.Is(err, ErrExecutionContextViewIntegrity) {
		t.Fatalf("want ErrExecutionContextViewIntegrity via GetByContextSnapshot, got %v", err)
	}
}

// TestResolveAndPersist_DoesNotMutateCanonicalSnapshot is section 9.I.
func TestResolveAndPersist_DoesNotMutateCanonicalSnapshot(t *testing.T) {
	store := NewMemoryStore()
	svc := ContextAssemblyService{Store: store}
	snap := orgSnapshot(6, "investigacion/research_worker_hourly", roleCatalogYAML())
	before := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	before.ID = snap.ID
	before.OrganizationID = snap.OrganizationID

	if _, err := svc.ResolveAndPersist(context.Background(), snap); err != nil {
		t.Fatal(err)
	}

	if len(snap.Segments) != len(before.Segments) {
		t.Fatal("segment count changed")
	}
	for i := range snap.Segments {
		if string(snap.Segments[i].Content) != string(before.Segments[i].Content) {
			t.Fatalf("segment %d content mutated by persistence", i)
		}
	}
	if snap.RenderedHash != before.RenderedHash {
		t.Fatal("RenderedHash changed by persistence")
	}
}
