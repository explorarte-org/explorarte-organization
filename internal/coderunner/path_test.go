package coderunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRealPathRejectsTraversalAndAbsolute(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../outside", "../../etc/passwd", "/etc/passwd", "a/../../b", "."} {
		if _, err := realPath(root, rel); err == nil {
			t.Fatalf("accepted unsafe path %q", rel)
		}
	}
}

func TestRealPathRejectsNestedTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := realPath(root, "a/b/../../../escape"); err == nil {
		t.Fatal("accepted nested traversal")
	}
}

func TestRealPathAllowsNotYetExistingFinalComponent(t *testing.T) {
	root := t.TempDir()
	p, err := realPath(root, "new/file.go")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "new", "file.go")
	if p != want {
		t.Fatalf("got %q want %q", p, want)
	}
}

func TestRealPathRejectsSymlinkedDirectoryEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link-dir")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if _, err := realPath(root, "link-dir/secret.go"); err == nil {
		t.Fatal("accepted escape through a symlinked directory")
	}
}

func TestRealPathRejectsSymlinkedFinalComponentEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.go")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "innocuous.go")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if _, err := realPath(root, "innocuous.go"); err == nil {
		t.Fatal("accepted escape through a final-component symlink")
	}
}

func TestRealPathRejectsWorkspacePrefixConfusion(t *testing.T) {
	root := t.TempDir()
	sibling := root + "-evil"
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sibling)
	if within(root, sibling) {
		t.Fatal("sibling directory sharing a string prefix must not count as contained")
	}
	if within(root, sibling+string(filepath.Separator)+"f") {
		t.Fatal("sibling path must not count as contained")
	}
}

func TestRealPathRejectsGitPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".git/config", ".git", ".git/hooks/pre-commit", "a/.git/config"} {
		if !structurallyDenied(rel, true) || !structurallyDenied(rel, false) {
			t.Fatalf("expected %q to be structurally denied", rel)
		}
	}
}

func TestGoModGoSumDeniedOnlyForMutation(t *testing.T) {
	if !structurallyDenied("go.mod", true) {
		t.Fatal("go.mod mutation must be denied")
	}
	if structurallyDenied("go.mod", false) {
		t.Fatal("go.mod read must be allowed")
	}
	if !structurallyDenied("go.sum", true) {
		t.Fatal("go.sum mutation must be denied")
	}
}

func TestWithinGitMetadataCatchesSymlinkedEscapeIntoGit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "notgit")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	real, err := realPath(root, "notgit/config")
	if err != nil {
		// realPath resolving the ancestor into root/.git already keeps it
		// "within root", so the .git check below is what must catch it.
		t.Fatalf("unexpected realPath error: %v", err)
	}
	if !withinGitMetadata(root, real) {
		t.Fatal("expected a symlink into .git to be caught as git metadata")
	}
}
