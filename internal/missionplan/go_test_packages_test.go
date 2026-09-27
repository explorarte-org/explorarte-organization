package missionplan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

// Smokes #20 to #22 (2026-09-27): the closure refused `go test ./...` because the goal named
// `go test ./internal/identifiers/...`. GO_TEST now names the changed packages and keeps the module.

func TestGoTestNamesTheChangedPackagesAndKeepsTheModule(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  []string
	}{
		{[]string{"internal/identifiers/identifiers_test.go"}, []string{"./internal/identifiers/...", "./..."}},
		{[]string{"internal/b/x.go", "internal/a/y_test.go", "internal/a/z.go"}, []string{"./internal/a/...", "./internal/b/...", "./..."}},
		{[]string{"main.go"}, []string{"./..."}},
		{nil, nil},
	} {
		if got := goTestPackages(c.files); !slices.Equal(got, c.want) {
			t.Errorf("goTestPackages(%v) = %v, want %v", c.files, got, c.want)
		}
	}
}

func TestTheTestGateDeclaresWhatTheTestOperationRuns(t *testing.T) {
	packages := []string{"./internal/identifiers/...", "./..."}
	var gate engineeringmission.RequiredGate
	for _, g := range requiredGatesTesting(packages) {
		if g.Type == engineeringmission.GateTest {
			gate = g
		}
	}
	if !slices.Equal(gate.Packages, packages) {
		t.Fatalf("GO_TEST gate packages=%v, want %v", gate.Packages, packages)
	}
	// The shared fixed gate set is not mutated.
	for _, g := range RequiredGates() {
		if len(g.Packages) != 0 {
			t.Fatalf("RequiredGates() was mutated: %+v", g)
		}
	}
}

// The command the code-runner builds from those packages is valid Go and runs.
func TestTheCodeRunnerRunsTheNamedPackagesAndTheModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go required")
	}
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"go.mod":                          "module fixture\n\ngo 1.25\n",
		"internal/identifiers/id.go":      "package identifiers\n\nfunc One() int { return 1 }\n",
		"internal/identifiers/id_test.go": "package identifiers\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	packages := goTestPackages([]string{"internal/identifiers/id_test.go"})
	result, err := (&coderunner.Executor{Workspace: dir}).ExecuteOperation(context.Background(), coderunner.Operation{Type: coderunner.GoTest, Packages: packages})
	if err != nil || !result.Success {
		t.Fatalf("go test %v: success=%v err=%v", packages, result.Success, err)
	}
	if !slices.Equal(result.Command, []string{"go", "test", "./internal/identifiers/...", "./..."}) {
		t.Fatalf("command=%v", result.Command)
	}
}

// Through Derive, the only place operations are made: the GO_TEST operation and the mission's GO_TEST
// gate carry the same packages, and a documentation-only mission keeps the module default.
func TestDeriveBindsTheTestOperationAndItsGate(t *testing.T) {
	request := docsRequest()
	request.Scope = ScopeInternalCode
	request.Changes = []Change{{Path: "internal/identifiers/identifiers_test.go", Intent: "one case", Patch: unifiedDiff("internal/identifiers/identifiers_test.go")}}
	derived, err := Derive(request)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := []string{"./internal/identifiers/...", "./..."}
	var ran []string
	for _, operation := range derived.Plan.Operations {
		if operation.Type == coderunner.GoTest {
			ran = operation.Packages
		}
	}
	if !slices.Equal(ran, want) {
		t.Fatalf("GO_TEST operation packages=%v, want %v", ran, want)
	}
	for _, gate := range derived.Policy.RequiredGates {
		if gate.Type == engineeringmission.GateTest && !sameSet(gate.Packages, want) {
			t.Fatalf("GO_TEST gate packages=%v, want %v", gate.Packages, want)
		}
	}

	docs, err := Derive(docsRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range docs.Plan.Operations {
		if operation.Type == coderunner.GoTest && operation.Packages != nil {
			t.Fatalf("a documentation-only mission names test packages: %v", operation.Packages)
		}
	}
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
