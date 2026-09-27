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

// Smokes #20 to #23 (2026-09-27): the closure refused `go test ./...`, then refused
// `go test ./internal/identifiers/... ./...`, because the goal named exactly
// `go test ./internal/identifiers/...`. The changed packages now get a GO_TEST of their own, and the
// whole module keeps its own.

func TestTheChangedPackagesAreNamedAsAGoalWouldNameThem(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  []string
	}{
		{[]string{"internal/identifiers/identifiers_test.go"}, []string{"./internal/identifiers/..."}},
		{[]string{"internal/b/x.go", "internal/a/y_test.go", "internal/a/z.go"}, []string{"./internal/a/...", "./internal/b/..."}},
		{[]string{"main.go"}, nil},
		{nil, nil},
	} {
		if got := changedTestPackages(c.files); !slices.Equal(got, c.want) {
			t.Errorf("changedTestPackages(%v) = %v, want %v", c.files, got, c.want)
		}
	}
}

func goTestOperations(plan coderunner.Plan) [][]string {
	var runs [][]string
	for _, operation := range plan.Operations {
		if operation.Type == coderunner.GoTest {
			runs = append(runs, operation.Packages)
		}
	}
	return runs
}

func goTestGates(policy engineeringmission.MissionPolicy) [][]string {
	var gates [][]string
	for _, gate := range policy.RequiredGates {
		if gate.Type == engineeringmission.GateTest {
			gates = append(gates, gate.Packages)
		}
	}
	return gates
}

// Through Derive, the only place operations are made: the literal run first, then the module, and a
// gate for each, so the promotion's gate check requires both.
func TestDeriveRunsTheLiteralPackagesAndThenTheModule(t *testing.T) {
	request := docsRequest()
	request.Scope = ScopeInternalCode
	request.Changes = []Change{{Path: "internal/identifiers/identifiers_test.go", Intent: "one case", Patch: unifiedDiff("internal/identifiers/identifiers_test.go")}}
	derived, err := Derive(request)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	runs := goTestOperations(derived.Plan)
	if len(runs) != 2 || !slices.Equal(runs[0], []string{"./internal/identifiers/..."}) || runs[1] != nil {
		t.Fatalf("GO_TEST runs=%v, want [./internal/identifiers/...] then the module default", runs)
	}
	gates := goTestGates(derived.Policy)
	if len(gates) != 2 || !slices.Equal(gates[0], []string{"./internal/identifiers/..."}) || gates[1] != nil {
		t.Fatalf("GO_TEST gates=%v, want one per run", gates)
	}
	// The shared fixed gate set is not mutated.
	for _, gate := range RequiredGates() {
		if len(gate.Packages) != 0 {
			t.Fatalf("RequiredGates() was mutated: %+v", gate)
		}
	}
}

func TestAMissionWithoutGoPackagesRunsTheModuleOnce(t *testing.T) {
	for _, request := range []Request{docsRequest(), func() Request {
		r := docsRequest()
		r.Scope = ScopeInternalCode
		r.Changes = []Change{{Path: "main.go", Intent: "x", Patch: unifiedDiff("main.go")}}
		return r
	}()} {
		derived, err := Derive(request)
		if err != nil {
			// A root-level Go file may be out of the request's scope; only in-scope requests matter here.
			continue
		}
		if runs := goTestOperations(derived.Plan); len(runs) != 1 || runs[0] != nil {
			t.Fatalf("GO_TEST runs=%v, want only the module default", runs)
		}
		if gates := goTestGates(derived.Policy); len(gates) != 1 || gates[0] != nil {
			t.Fatalf("GO_TEST gates=%v, want only the module gate", gates)
		}
	}
}

// Both commands are valid Go and run through the code-runner's executor, and the first is literally
// what a goal names.
func TestTheCodeRunnerRunsTheLiteralCommandAndTheModule(t *testing.T) {
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
	executor := &coderunner.Executor{Workspace: dir}
	for packages, want := range map[string][]string{
		"literal": {"go", "test", "./internal/identifiers/..."},
		"module":  {"go", "test", "./..."},
	} {
		op := coderunner.Operation{Type: coderunner.GoTest}
		if packages == "literal" {
			op.Packages = changedTestPackages([]string{"internal/identifiers/id_test.go"})
		}
		result, err := executor.ExecuteOperation(context.Background(), op)
		if err != nil || !result.Success || !slices.Equal(result.Command, want) {
			t.Fatalf("%s: command=%v success=%v err=%v, want %v", packages, result.Command, result.Success, err, want)
		}
	}
}
