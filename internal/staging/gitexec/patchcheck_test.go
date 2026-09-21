package gitexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

// testTable is shaped like the production file root 1007 tried to patch: a
// table of positional struct literals, indented with tabs.
const testTable = "package identifiers\n\nfunc cases() [][]string {\n\treturn [][]string{\n\t\t{\"no digits\", \"no numbers here\"},\n\t\t{\"empty input\", \"\"},\n\t}\n}\n"

type patchRig struct {
	backend   *Backend
	config    staging.RepositoryConfig
	repo      string
	commit    string
	workspace string
}

func newPatchRig(t *testing.T) *patchRig {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	workspaceRoot := filepath.Join(root, "workspaces")
	mustMkdir(t, repo)
	mustMkdir(t, workspaceRoot)
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "identifiers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "identifiers", "table.go"), []byte(testTable), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "base")
	backend, err := New("git", workspaceRoot, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	config := staging.RepositoryConfig{ID: "repo", Path: repo, Enabled: true, AllowedTargetRefs: []string{"refs/heads/main"}}
	return &patchRig{backend: backend, config: config, repo: repo, commit: strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")), workspace: workspaceRoot}
}

const goodPatch = "diff --git a/internal/identifiers/table.go b/internal/identifiers/table.go\n" +
	"--- a/internal/identifiers/table.go\n+++ b/internal/identifiers/table.go\n" +
	"@@ -5,3 +5,4 @@\n" +
	" \t\t{\"no digits\", \"no numbers here\"},\n" +
	" \t\t{\"empty input\", \"\"},\n" +
	"+\t\t{\"digits adjacent to letters\", \"abc123def45\"},\n" +
	" \t}\n"

func (r *patchRig) check(t *testing.T, patch string) staging.PatchCheck {
	t.Helper()
	verdict, err := r.backend.CheckPatch(context.Background(), r.config, r.commit, patch)
	if err != nil {
		t.Fatalf("CheckPatch: %v", err)
	}
	return verdict
}

func TestCheckPatchAcceptsAPatchThatAppliesToTheFrozenTree(t *testing.T) {
	rig := newPatchRig(t)
	if verdict := rig.check(t, goodPatch); !verdict.Applies {
		t.Fatalf("a patch that applies was rejected: %+v", verdict)
	}
}

// Root 1007's patch: placeholder hunk coordinates. git says exactly what the
// code-runner reported five times.
func TestCheckPatchRefusesThePlaceholderHunkOfRoot1007(t *testing.T) {
	rig := newPatchRig(t)
	patch := "diff --git a/internal/identifiers/table.go b/internal/identifiers/table.go\n" +
		"--- a/internal/identifiers/table.go\n+++ b/internal/identifiers/table.go\n" +
		"@@ -X,X +X,X @@\n \t\t{\n+\t\t\tname: \"digits adjacent to letters\",\n \t\t},\n"
	verdict := rig.check(t, patch)
	if verdict.Applies || !strings.Contains(verdict.Detail, "corrupt patch") {
		t.Fatalf("verdict = %+v, want git's own 'corrupt patch' diagnostic", verdict)
	}
}

func TestCheckPatchRefusesAWellFormedHunkWhoseContextDoesNotExist(t *testing.T) {
	rig := newPatchRig(t)
	patch := "diff --git a/internal/identifiers/table.go b/internal/identifiers/table.go\n" +
		"--- a/internal/identifiers/table.go\n+++ b/internal/identifiers/table.go\n" +
		"@@ -5,2 +5,3 @@\n" +
		" \t\t{name: \"no digits\", text: \"no numbers here\"},\n" +
		" \t\t{name: \"empty input\", text: \"\"},\n" +
		"+\t\t{name: \"digits adjacent to letters\", text: \"abc123def45\"},\n"
	verdict := rig.check(t, patch)
	if verdict.Applies || verdict.Detail == "" {
		t.Fatalf("a hunk over context that is not in the file was accepted: %+v", verdict)
	}
}

func TestCheckPatchRefusesAPatchToAFileThatDoesNotExist(t *testing.T) {
	rig := newPatchRig(t)
	patch := "diff --git a/internal/identifiers/absent.go b/internal/identifiers/absent.go\n" +
		"--- a/internal/identifiers/absent.go\n+++ b/internal/identifiers/absent.go\n@@ -1,2 +1,3 @@\n a\n b\n+c\n"
	if verdict := rig.check(t, patch); verdict.Applies {
		t.Fatalf("a patch to a file absent at the commit was accepted: %+v", verdict)
	}
}

// The check must not disturb anything another component might be using: the
// repository's index, working tree, refs and object store are byte-for-byte what
// they were, and the scratch index is gone.
func TestCheckPatchLeavesTheRepositoryUntouched(t *testing.T) {
	rig := newPatchRig(t)
	// Reading status refreshes the index's stat cache; do it BEFORE the snapshot so
	// the comparison isolates the check.
	statusBefore := git(t, rig.repo, "status", "--porcelain")
	indexBefore := git(t, rig.repo, "ls-files", "-s")
	before := snapshotTree(t, rig.repo)
	for _, patch := range []string{goodPatch, "@@ -X,X +X,X @@\n"} {
		_, _ = rig.backend.CheckPatch(context.Background(), rig.config, rig.commit, patch)
	}
	after := snapshotTree(t, rig.repo)
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the repository changed (files, sizes or modification times differ)")
	}
	if git(t, rig.repo, "status", "--porcelain") != statusBefore || git(t, rig.repo, "ls-files", "-s") != indexBefore {
		t.Fatal("the repository's own index or working tree changed")
	}
	if content, err := os.ReadFile(filepath.Join(rig.repo, "internal", "identifiers", "table.go")); err != nil || string(content) != testTable {
		t.Fatalf("the working file changed: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(rig.workspace, ".control", "patchcheck-*")); len(leftovers) != 0 {
		t.Fatalf("scratch indexes were left behind: %v", leftovers)
	}
}

// It is checked against the FROZEN commit, not whatever the working tree holds
// now: a file edited in the working tree after the freeze changes nothing.
func TestCheckPatchIsAgainstTheCommitNotTheWorkingTree(t *testing.T) {
	rig := newPatchRig(t)
	if err := os.WriteFile(filepath.Join(rig.repo, "internal", "identifiers", "table.go"), []byte("completely different\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if verdict := rig.check(t, goodPatch); !verdict.Applies {
		t.Fatalf("the verdict depends on the working tree: %+v", verdict)
	}
}

// A check that could not run is an error, never a verdict about the patch.
func TestCheckPatchDistinguishesAVerdictFromAFailureToCheck(t *testing.T) {
	rig := newPatchRig(t)
	ctx := context.Background()
	unknown := strings.Repeat("a", 40)
	if _, err := rig.backend.CheckPatch(ctx, rig.config, unknown, goodPatch); err == nil {
		t.Fatal("an unknown commit produced a verdict")
	}
	if _, err := rig.backend.CheckPatch(ctx, rig.config, "main", goodPatch); !errors.Is(err, staging.ErrInvalidInput) {
		t.Fatalf("a symbolic ref was accepted as a commit: %v", err)
	}
	if _, err := rig.backend.CheckPatch(ctx, rig.config, rig.commit, "  \n"); !errors.Is(err, staging.ErrInvalidInput) {
		t.Fatalf("an empty patch produced a verdict: %v", err)
	}
	notARepository := rig.config
	notARepository.Path = t.TempDir()
	if _, err := rig.backend.CheckPatch(ctx, notARepository, rig.commit, goodPatch); err == nil {
		t.Fatal("a directory that is not a repository produced a verdict")
	}
}

func TestReadFileReturnsTheExactContentAtTheCommit(t *testing.T) {
	rig := newPatchRig(t)
	ctx := context.Background()
	got, err := rig.backend.ReadFile(ctx, rig.config, rig.commit, "internal/identifiers/table.go", 1<<20)
	if err != nil || string(got) != testTable {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	// A later commit does not change what the frozen one says.
	if err := os.WriteFile(filepath.Join(rig.repo, "internal", "identifiers", "table.go"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, rig.repo, "commit", "-am", "later")
	if again, err := rig.backend.ReadFile(ctx, rig.config, rig.commit, "internal/identifiers/table.go", 1<<20); err != nil || string(again) != testTable {
		t.Fatalf("the frozen content moved: %q, %v", again, err)
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../b", "-flag", "a:b", "a\\b", "a//b"} {
		if _, err := rig.backend.ReadFile(ctx, rig.config, rig.commit, bad, 1<<20); !errors.Is(err, staging.ErrInvalidInput) {
			t.Errorf("path %q: err = %v, want ErrInvalidInput", bad, err)
		}
	}
	if _, err := rig.backend.ReadFile(ctx, rig.config, rig.commit, "internal/identifiers/absent.go", 1<<20); err == nil {
		t.Fatal("a missing file was read")
	}
	if _, err := rig.backend.ReadFile(ctx, rig.config, rig.commit, "internal/identifiers/table.go", 10); err == nil {
		t.Fatal("the size limit was not enforced")
	}
}

func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var entries []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			entries = append(entries, path+"|"+info.ModTime().String()+"|"+string(rune(info.Size())))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	return entries
}

// git does not encode "the patch is wrong" in its exit status: a corrupt patch
// exits 128, the same status as a broken repository. The classifier reads git's own
// diagnostic instead.
func TestPatchDiagnosticClassification(t *testing.T) {
	for _, tc := range []struct {
		code    int
		message string
		verdict bool
	}{
		{128, "error: corrupt patch at <stdin>:4", true},
		{1, "error: patch failed: internal/x.go:5\nerror: internal/x.go: patch does not apply", true},
		{1, "error: internal/x.go: does not exist in index", true},
		{128, "fatal: not a git repository (or any of the parent directories): .git", false},
		{128, "error: something\nfatal: mixed", false},
		{129, "error: unknown option", false},
		{2, "error: corrupt patch", false},
		{1, "", false},
	} {
		if got := isPatchDiagnostic(tc.code, tc.message); got != tc.verdict {
			t.Errorf("isPatchDiagnostic(%d, %q) = %v, want %v", tc.code, tc.message, got, tc.verdict)
		}
	}
}
