package canonical

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func TestCanonicalProviderLoadsValidatedAllowlistDeterministically(t *testing.T) {
	root := filepath.Join("..", "..", "..", "docs", "canonical")
	provider, err := New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.PrecedenceHash == "" || first.BundleHash == "" || first.BundleHash != second.BundleHash {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	for _, source := range first.Sources {
		if source.LogicalName == "docs/canonical/source-manifest.yaml" {
			t.Fatal("source manifest must not be indiscriminately included")
		}
	}
}

func TestImportedSkillsAreNotActive(t *testing.T) {
	root := filepath.Join("..", "..", "..", "docs", "canonical")
	provider, err := NewSkillProvider(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"crear-perfil-y-buscar-skills", "pulir-embeddings-mal-troceados", "revisar-publicacion-cerebro-empresa", "auditar-ux-y-accesibilidad"} {
		record, ok := provider.Record(id)
		if !ok {
			t.Fatalf("missing imported skill %s", id)
		}
		if record.Lifecycle == contextengine.SkillActive {
			t.Fatalf("%s became active", id)
		}
		if record.RoleID == "" || record.Department == "" {
			t.Fatalf("%s lost canonical owner identity: %+v", id, record)
		}
		_, getErr := provider.GetActiveForRole(t.Context(), "explorarte", record.RoleID, id)
		if contextengine.ReasonOf(getErr) != contextengine.ReasonSkillNotActive {
			t.Fatalf("%s error=%v", id, getErr)
		}
	}
	active, err := provider.ListActiveForRole(t.Context(), "explorarte", "ingenieria_ia/frontend")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("active=%+v", active)
	}
}

func TestCanonicalValidateReportsDrift(t *testing.T) {
	provider, err := New(filepath.Join("..", "..", "..", "docs", "canonical"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := provider.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.Validate(t.Context(), "0"+bundle.PrecedenceHash[1:], bundle.BundleHash); contextengine.ReasonOf(err) != contextengine.ReasonPrecedenceHashMismatch {
		t.Fatalf("error=%v", err)
	}
	if err = provider.Validate(t.Context(), bundle.PrecedenceHash, "0"+bundle.BundleHash[1:]); contextengine.ReasonOf(err) != contextengine.ReasonCanonicalBundleDrift {
		t.Fatalf("error=%v", err)
	}
	if errors.Is(err, contextengine.ErrDatabaseUnavailable) {
		t.Fatal("drift misclassified as operational")
	}
}
