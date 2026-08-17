package coderunner

import (
	"context"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type functionalWorkspace struct{ path string }

func (w functionalWorkspace) Open(context.Context, tasks.ClaimedTask, string) (string, int64, error) {
	return w.path, 1, nil
}
func (w functionalWorkspace) Seal(context.Context, int64, tasks.ClaimedTask, string) (any, error) {
	return os.ReadFile(filepath.Join(w.path, "fixture.go"))
}

func TestWorkerFunctionalTypedExecutionInIsolatedWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("package fixture\n\nfunc Value() int { return 1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "runner@example.invalid"}, {"config", "user.name", "runner"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	q := &queueFake{instructions: `{"schema_version":"code-runner-execution/v1","operations":[{"type":"APPLY_PATCH","patch":"diff --git a/fixture.go b/fixture.go\n--- a/fixture.go\n+++ b/fixture.go\n@@ -1,3 +1,3 @@\n package fixture\n \n-func Value() int { return 1 }\n+func Value() int { return 2 }\n"},{"type":"GOFMT","path":"fixture.go"},{"type":"GO_TEST","packages":["./..."]},{"type":"GIT_DIFF"},{"type":"GIT_STATUS"}]}`}
	w := Worker{Queue: q, Executor: &Executor{MaxOutput: 1 << 20}, Workspace: functionalWorkspace{path: root}, WorkerID: "runner", HolderPrincipalID: "42", LeaseDuration: time.Second}
	n, e := w.RunOnce(context.Background())
	if e != nil || n != 1 || q.recorded != 1 {
		t.Fatalf("worker n=%d err=%v records=%d", n, e, q.recorded)
	}
	if _, err := os.Stat(filepath.Join(root, "fixture.go")); err != nil {
		t.Fatal(err)
	}
}
