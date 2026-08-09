package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"gopkg.in/yaml.v3"
)

func canonicalDirForTest(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "docs", "canonical")
}

func TestLoadCurrentCanonicalDocuments(t *testing.T) {
	loader, err := NewLoader(canonicalDirForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, report, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v (%+v)", err, report)
	}
	if snapshot.Organization.ID != "explorarte" {
		t.Fatalf("organization=%q", snapshot.Organization.ID)
	}
	if snapshot.Counts.OperationalUnits != 7 || snapshot.Counts.TransversalUnits != 2 {
		t.Fatalf("unit counts=%+v", snapshot.Counts)
	}
	if snapshot.Counts.Roles != 48 || snapshot.Counts.ImportedRoles != 45 || snapshot.Counts.ProposedRoles != 3 {
		t.Fatalf("role counts=%+v", snapshot.Counts)
	}
	documentHashes := make(map[string]string, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		documentHashes[document.Path] = document.SemanticHash
	}
	if documentHashes["model-egress-policy.yaml"] == "" || documentHashes["capability-matrix.yaml"] == "" {
		t.Fatalf("missing branch 09 document hashes: %+v", documentHashes)
	}
	egress, err := modelegress.LoadCanonicalPolicy(canonicalDirForTest(t), modelegress.ProductiveLoadOptions([]string{"alibaba_token_plan_via_claude_code", "deepseek", "openai_compatible", "gemini"}))
	if err != nil {
		t.Fatal(err)
	}
	if documentHashes[modelegress.PolicyFileName] != egress.CanonicalHash {
		t.Fatalf("registry/model-egress semantic hash mismatch: %s != %s", documentHashes[modelegress.PolicyFileName], egress.CanonicalHash)
	}
	for _, id := range []string{"empresa/ceo_observer", "investigacion/research_worker_hourly", "ingenieria_ia/razonamiento_logico"} {
		role := findRole(snapshot, id)
		if role == nil || role.Enabled || role.Executable {
			t.Fatalf("proposed role %s=%+v", id, role)
		}
	}
}

func TestCanonicalHashIgnoresSemanticOrderingAndLineEndings(t *testing.T) {
	first := loadTestSnapshot(t, canonicalDirForTest(t))
	dir := copyCanonical(t)
	path := filepath.Join(dir, "role-catalog.yaml")
	var document roleCatalogDocument
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	for left, right := 0, len(document.Roles)-1; left < right; left, right = left+1, right-1 {
		document.Roles[left], document.Roles[right] = document.Roles[right], document.Roles[left]
	}
	body, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(string(body) + "\r\n")
	if err = os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	second := loadTestSnapshot(t, dir)
	if first.CanonicalHash != second.CanonicalHash {
		t.Fatalf("hash changed: %s != %s", first.CanonicalHash, second.CanonicalHash)
	}
}

func TestSourceManifestIsNeverDereferenced(t *testing.T) {
	dir := copyCanonical(t)
	path := filepath.Join(dir, "source-manifest.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte("schema_version: 0.1.0\ngenerated_at: 2026-08-04T00:00:00Z\nfiles:\n- path: /definitely/not/present/organizacion/test/PERFIL.md\n  sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n  bytes: 1\n")
	if err = os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = mustLoader(t, dir).Load(); err != nil {
		t.Fatalf("source manifest path was dereferenced or rejected: %v", err)
	}
}

func copyCanonical(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(canonicalDirForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(canonicalDirForTest(t), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, entry.Name()), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func loadTestSnapshot(t *testing.T, dir string) Snapshot {
	t.Helper()
	snapshot, report, err := mustLoader(t, dir).Load()
	if err != nil {
		t.Fatalf("load: %v %+v", err, report)
	}
	return snapshot
}
func mustLoader(t *testing.T, dir string) *Loader {
	t.Helper()
	loader, err := NewLoader(dir)
	if err != nil {
		t.Fatal(err)
	}
	return loader
}
func findRole(snapshot Snapshot, id string) *Role {
	for i := range snapshot.Roles {
		if snapshot.Roles[i].ID == id {
			return &snapshot.Roles[i]
		}
	}
	return nil
}

func TestStrictYAMLRejectsAnchorsAndUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "anchor",
			body: "schema_version: &version 0.1.0\ndocument_status: branch_0_candidate\norganization: {}\nexecutive: {}\noperational_departments: []\ntransversal_units: []\ndaily_cycle: {}\ncounts: {}\n",
		},
		{
			name: "alias",
			body: "schema_version: &version 0.1.0\ndocument_status: *version\norganization: {}\nexecutive: {}\noperational_departments: []\ntransversal_units: []\ndaily_cycle: {}\ncounts: {}\n",
		},
		{
			name: "unknown field",
			body: "schema_version: 0.1.0\ndocument_status: branch_0_candidate\norganization: {}\nexecutive: {}\noperational_departments: []\ntransversal_units: []\ndaily_cycle: {}\ncounts: {}\nunexpected: true\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var document organizationDocument
			if err := decodeStrictYAML([]byte(tc.body), &document); err == nil {
				t.Fatalf("decodeStrictYAML accepted %s", tc.name)
			}
		})
	}
}
