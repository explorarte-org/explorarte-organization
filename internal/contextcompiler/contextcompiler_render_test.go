package contextcompiler

import (
	"bytes"
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// TestResolveProviderContext_GenericFallbackMatchesCanonicalPortableRender
// proves a role/task class with no registered ContextProfile behaves exactly
// as before this mission: the shared resolver falls back to the canonical
// PortableRenderer bytes, unprojected.
func TestResolveProviderContext_GenericFallbackMatchesCanonicalPortableRender(t *testing.T) {
	snap := testSnapshot("empresa/ceo", roleCatalogYAML())

	resolved, err := ResolveProviderContext(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.FellBack {
		t.Fatal("expected an unregistered task class to fall back to canonical")
	}
	if resolved.FallbackReason != "task_class_not_projected" {
		t.Fatalf("unexpected fallback reason: %s", resolved.FallbackReason)
	}

	canonical, err := contextengine.NewRenderer().Render(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resolved.Bytes, canonical) {
		t.Fatalf("generic fallback bytes diverged from canonical PortableRenderer:\nresolved=%s\ncanonical=%s", resolved.Bytes, canonical)
	}
	if resolved.Digest != contextengine.DigestCanonicalBytes(canonical) {
		t.Fatalf("generic fallback digest diverged from canonical digest: got=%s want=%s", resolved.Digest, contextengine.DigestCanonicalBytes(canonical))
	}
}

// TestResolveProviderContext_ProjectedResearchProfileDiffersFromCanonical is
// the critical regression test: it exercises the real research.corpus_curate/v1
// profile end to end (CompileForTaskClass -> profile applied -> ProviderRender
// produced), and explicitly proves the projected bytes are NOT a silent
// fallback to canonical -- FellBack must be false, and the projected bytes
// must differ from what the legacy canonical PortableRenderer would have
// produced for the same snapshot (the role-catalog projection actually
// shrinks content).
func TestResolveProviderContext_ProjectedResearchProfileDiffersFromCanonical(t *testing.T) {
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())

	resolved, err := ResolveProviderContext(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.FellBack {
		t.Fatalf("projected research.corpus_curate/v1 case must not fall back to canonical, got reason=%q", resolved.FallbackReason)
	}
	if resolved.ProviderRender.Version != contextengine.ProviderRenderVersionV2 {
		t.Fatalf("expected ProviderRender to be populated, got version=%q", resolved.ProviderRender.Version)
	}
	if len(resolved.Bytes) == 0 {
		t.Fatal("resolved provider-visible bytes are empty")
	}

	canonical, err := contextengine.NewRenderer().Render(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(resolved.Bytes, canonical) {
		t.Fatal("projected provider-visible bytes must not equal the legacy canonical PortableRenderer bytes -- projection had no effect")
	}
	if resolved.Digest == contextengine.DigestCanonicalBytes(canonical) {
		t.Fatal("projected provider-visible digest must not equal the canonical digest")
	}
}

// TestResolveProviderContext_Determinism proves the resolver is a pure
// function of its input: the same canonical snapshot resolved twice
// (independently, no pointer/cache reuse) produces byte-identical output and
// the same digest, for both the generic-fallback and projected-research
// cases; and that changing projected content changes the digest.
func TestResolveProviderContext_Determinism(t *testing.T) {
	for _, actorRoleID := range []string{"empresa/ceo", "investigacion/research_worker_hourly"} {
		t.Run(actorRoleID, func(t *testing.T) {
			first, err := ResolveProviderContext(context.Background(), testSnapshot(actorRoleID, roleCatalogYAML()))
			if err != nil {
				t.Fatal(err)
			}
			second, err := ResolveProviderContext(context.Background(), testSnapshot(actorRoleID, roleCatalogYAML()))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.Bytes, second.Bytes) || first.Digest != second.Digest {
				t.Fatalf("resolver is not deterministic for %s: first=%x/%s second=%x/%s", actorRoleID, first.Bytes, first.Digest, second.Bytes, second.Digest)
			}
		})
	}

	// Changing the requesting actor's OWN catalog entry (as opposed to an
	// unrelated role's entry) is what must flow through
	// RoleCatalogSelfEntry's projection and change the resolved digest --
	// appending an unrelated role would not, since the projection extracts
	// only the actor's own entry.
	changed := bytes.Replace(roleCatalogYAML(), []byte("Research worker contract."), []byte("Research worker contract, revised."), 1)
	if bytes.Equal(changed, roleCatalogYAML()) {
		t.Fatal("test bug: replacement did not change the fixture")
	}
	baseline, err := ResolveProviderContext(context.Background(), testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML()))
	if err != nil {
		t.Fatal(err)
	}
	altered, err := ResolveProviderContext(context.Background(), testSnapshot("investigacion/research_worker_hourly", changed))
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Digest == altered.Digest {
		t.Fatal("changing projected content must change the digest")
	}
}
