package coderunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitPatchInit(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "in.go"), []byte("package fixture\n\nfunc V() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "runner@example.invalid"}, {"config", "user.name", "runner"}, {"add", "-A"}, {"commit", "-m", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestExtractPatchPathsRejectsUnsafeTargets(t *testing.T) {
	cases := map[string]string{
		"parent traversal": "diff --git a/../outside b/../outside\n--- a/../outside\n+++ b/../outside\n@@ -1 +1 @@\n-x\n+y\n",
		"git config":       "diff --git a/.git/config b/.git/config\n--- a/.git/config\n+++ b/.git/config\n@@ -1 +1 @@\n-x\n+y\n",
		"git hooks":        "diff --git a/.git/hooks/x b/.git/hooks/x\n--- a/.git/hooks/x\n+++ b/.git/hooks/x\n@@ -1 +1 @@\n-x\n+y\n",
		"absolute path":    "diff --git a//etc/passwd b//etc/passwd\n--- a//etc/passwd\n+++ b//etc/passwd\n@@ -1 +1 @@\n-x\n+y\n",
		"go.mod mutation":  "diff --git a/go.mod b/go.mod\n--- a/go.mod\n+++ b/go.mod\n@@ -1 +1 @@\n-x\n+y\n",
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validatePatchPaths(patch); err == nil {
				t.Fatalf("accepted unsafe patch: %s", name)
			}
		})
	}
}

func TestExtractPatchPathsAcceptsOrdinaryFile(t *testing.T) {
	patch := "diff --git a/pkg/x.go b/pkg/x.go\n--- a/pkg/x.go\n+++ b/pkg/x.go\n@@ -1 +1 @@\n-x\n+y\n"
	if err := validatePatchPaths(patch); err != nil {
		t.Fatal(err)
	}
}

// TestApplyPatchRejectsUnsafePathsBeforeMutation proves the executor never
// hands a patch touching a denied path to git at all: the workspace is left
// completely untouched (git status stays clean) and no infinite-retry loop
// is possible because the rejection is a deterministic Result, not an error
// from Execute.
func TestApplyPatchRejectsUnsafePathsBeforeMutation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	root := t.TempDir()
	gitPatchInit(t, root)
	e := Executor{Workspace: root}
	for name, patch := range map[string]string{
		"traversal": "diff --git a/../outside b/../outside\n--- a/../outside\n+++ b/../outside\n@@ -1,3 +1,3 @@\n package fixture\n \n-func V() int { return 1 }\n+func V() int { return 2 }\n",
		"absolute":  "diff --git a//etc/passwd b//etc/passwd\n--- a//etc/passwd\n+++ b//etc/passwd\n@@ -1,3 +1,3 @@\n package fixture\n \n-func V() int { return 1 }\n+func V() int { return 2 }\n",
		"git-hooks": "diff --git a/.git/hooks/pre-commit b/.git/hooks/pre-commit\n--- a/.git/hooks/pre-commit\n+++ b/.git/hooks/pre-commit\n@@ -0,0 +1 @@\n+evil\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, err := e.ExecuteOperation(context.Background(), Operation{Type: ApplyPatch, Patch: patch})
			if err == nil && r.Success {
				t.Fatalf("unsafe patch %q was accepted", name)
			}
			status := gitStatusOutput(t, root)
			if strings.TrimSpace(status) != "" {
				t.Fatalf("workspace mutated by rejected patch %q: %q", name, status)
			}
		})
	}
}

// TestApplyPatchCheckFailureLeavesWorkspaceUntouched proves
// `git apply --check` gates the real apply: a patch whose context does not
// match the file's actual content is rejected without mutating anything.
func TestApplyPatchCheckFailureLeavesWorkspaceUntouched(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	root := t.TempDir()
	gitPatchInit(t, root)
	badPatch := "diff --git a/in.go b/in.go\n--- a/in.go\n+++ b/in.go\n@@ -1,3 +1,3 @@\n package fixture\n \n-func V() int { return 999 }\n+func V() int { return 2 }\n"
	e := Executor{Workspace: root}
	r, err := e.ExecuteOperation(context.Background(), Operation{Type: ApplyPatch, Patch: badPatch})
	if err != nil {
		t.Fatal(err)
	}
	if r.Success {
		t.Fatal("apply --check should have failed (context mismatch)")
	}
	status := gitStatusOutput(t, root)
	if strings.TrimSpace(status) != "" {
		t.Fatalf("workspace mutated despite failed check: %q", status)
	}
}

func gitStatusOutput(t *testing.T, dir string) string {
	t.Helper()
	c := exec.Command("git", "status", "--short")
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v %s", err, out)
	}
	return string(out)
}
