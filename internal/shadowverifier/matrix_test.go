package shadowverifier

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const canonicalDir = "../../docs/canonical"

func TestLoadMatrixFromCanonicalRepository(t *testing.T) {
	matrix, err := LoadMatrix(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	if matrix.DefaultPolicy != "deny" {
		t.Fatalf("default policy=%q", matrix.DefaultPolicy)
	}
	if len(matrix.Hash) != 64 {
		t.Fatalf("hash=%q", matrix.Hash)
	}
	for _, capability := range []string{"code.commit", "model.invoke", "organization.activate_skill", "cell.read_clinical_data"} {
		if _, ok := matrix.Capabilities[capability]; !ok {
			t.Fatalf("canonical matrix is missing capability %q", capability)
		}
	}
	if !containsString(matrix.HardDenies["*"], "cell.read_clinical_data") {
		t.Fatal("global hard deny for cell.read_clinical_data missing")
	}
	if !containsString(matrix.HardDenies["owner"], "model.invoke") {
		t.Fatal("owner hard deny for model.invoke missing")
	}
	if !containsString(matrix.Grants["owner"], "*") {
		t.Fatal("owner wildcard grant missing")
	}
	ids := matrix.CapabilityIDs()
	if len(ids) != len(matrix.Capabilities) || !sorted(ids) {
		t.Fatalf("capability ids not sorted/complete: %v", ids)
	}
}

func TestLoadLeaderWorkerMapFromCanonicalRepository(t *testing.T) {
	leaderMap, err := LoadLeaderWorkerMap(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaderMap.Departments) != 4 {
		t.Fatalf("departments=%d want 4", len(leaderMap.Departments))
	}
	for _, department := range leaderMap.Departments {
		if department.Leader == "" || len(department.Workers) == 0 {
			t.Fatalf("department %+v incomplete", department)
		}
	}
}

func TestLoadMatrixRejectsInvalidDocuments(t *testing.T) {
	writeMatrix := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "capability-matrix.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	cases := []struct {
		name string
		body string
	}{
		{"allow default", "schema_version: 0.1.0\ndocument_status: branch_0_candidate\ndefault_policy: allow\ncapabilities:\n- id: a.b\n  risk: low\ngrants: {}\nhard_denies: {}\n"},
		{"grant unknown capability", "schema_version: 0.1.0\ndocument_status: branch_0_candidate\ndefault_policy: deny\ncapabilities:\n- id: a.b\n  risk: low\ngrants:\n  owner:\n  - missing.capability\nhard_denies: {}\n"},
		{"hard deny unknown authority", "schema_version: 0.1.0\ndocument_status: branch_0_candidate\ndefault_policy: deny\ncapabilities:\n- id: a.b\n  risk: low\ngrants:\n  owner:\n  - a.b\nhard_denies:\n  fantasma:\n  - a.b\n"},
		{"bad approval mode", "schema_version: 0.1.0\ndocument_status: branch_0_candidate\ndefault_policy: deny\ncapabilities:\n- id: a.b\n  risk: low\n  approval: sometimes\ngrants: {}\nhard_denies: {}\n"},
		{"unknown key", "schema_version: 0.1.0\ndocument_status: branch_0_candidate\ndefault_policy: deny\ncapabilities:\n- id: a.b\n  risk: low\ngrants: {}\nhard_denies: {}\nsurprise: true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadMatrix(writeMatrix(t, tc.body))
			if !errors.Is(err, ErrMatrixInvalid) {
				t.Fatalf("err=%v want ErrMatrixInvalid", err)
			}
		})
	}
	if _, err := LoadMatrix(t.TempDir()); !errors.Is(err, ErrMatrixUnavailable) {
		t.Fatalf("err=%v want ErrMatrixUnavailable", err)
	}
}

func sorted(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] < values[i-1] {
			return false
		}
	}
	return true
}
