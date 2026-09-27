package coderunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

// Root 1428 (smoke #17): the CEO closure refused to close because the evidence said GO_TEST exited 0
// without saying what ran, and that one file changed without saying which. The evidence now records
// the argv every gate executed and, from the seal itself, the paths the candidate changed.

func TestEveryGateRecordsTheCommandItRan(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go required")
	}
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"go.mod":      "module fixture\n\ngo 1.25\n",
		"add.go":      "package fixture\n\nfunc Add(a, b int) int { return a + b }\n",
		"add_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executor := &Executor{Workspace: dir}
	for _, c := range []struct {
		op   Operation
		want []string
	}{
		// No packages is the whole module: the evidence must say so, not leave it implied.
		{Operation{Type: GoTest}, []string{"go", "test", "./..."}},
		{Operation{Type: GoTest, Packages: []string{"./..."}, Race: true}, []string{"go", "test", "./...", "-race"}},
		{Operation{Type: GoBuild}, []string{"go", "build", "./..."}},
		{Operation{Type: GoVet, Packages: []string{"./..."}}, []string{"go", "vet", "./..."}},
	} {
		result, err := executor.ExecuteOperation(context.Background(), c.op)
		if err != nil {
			t.Fatalf("%s: %v", c.op.Type, err)
		}
		if !result.Success || !slices.Equal(result.Command, c.want) {
			t.Errorf("%s ran %v (success=%v), want %v", c.op.Type, result.Command, result.Success, c.want)
		}
	}
}

func TestTheEvidenceStatesTheCommandsAndTheSealedPaths(t *testing.T) {
	count := 1
	sealed := staging.Workspace{ID: 12, ChangedFileCount: &count, ChangedPaths: []string{"internal/identifiers/identifiers_test.go"}}
	ops := []Operation{{Type: ApplyPatch}, {Type: GoTest}, {Type: Fitness}}
	results := []Result{
		{Type: ApplyPatch, Success: true},
		{Type: GoTest, Success: true, Command: []string{"go", "test", "./..."}},
		{Type: Fitness, Success: true, Command: []string{"make", "test-kernel-governance-fitness", "test-executive-fitness"}},
	}
	evidence := buildAttemptEvidence(1441, 523, ops, results, sealed, executionEnvironment{})
	if !slices.Equal(evidence.ChangedFiles.Paths, []string{"internal/identifiers/identifiers_test.go"}) {
		t.Fatalf("changed paths=%v, want the sealed path", evidence.ChangedFiles.Paths)
	}
	commands := map[string][]string{}
	for _, check := range evidence.ChecksRun {
		commands[check.Type] = check.Command
	}
	if !slices.Equal(commands["GO_TEST"], []string{"go", "test", "./..."}) || len(commands["FITNESS"]) == 0 {
		t.Fatalf("check commands=%v, want what each gate ran", commands)
	}

	// Paths that do not account for every sealed change are not recorded: a partial list would read
	// as "only these changed".
	two := 2
	sealed.ChangedFileCount = &two
	if partial := buildAttemptEvidence(1441, 523, ops, results, sealed, executionEnvironment{}); partial.ChangedFiles.Paths != nil {
		t.Fatalf("a path list short of the sealed count was recorded: %v", partial.ChangedFiles.Paths)
	}
}
