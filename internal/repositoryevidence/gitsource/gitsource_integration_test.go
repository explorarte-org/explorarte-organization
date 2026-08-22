//go:build integration

package gitsource_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence/gitsource"
)

// Reading a repository at an exact commit is a claim about git's behaviour,
// not about Go's, so it is made against a real repository with two commits
// where the same file says different things.
func TestReadingIsAlwaysAboutTheCommitAsked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, "internal", "executive"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "internal", "executive", "orchestrator.go"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-b", "main")
	write("package executive\n\nfunc driveDepartments() {}\n")
	run("add", "--", "internal/executive/orchestrator.go")
	run("commit", "-m", "first")
	first := run("rev-parse", "HEAD")
	write("package executive\n\nfunc driveDepartmentsRenamed() {}\n")
	run("add", "--", "internal/executive/orchestrator.go")
	run("commit", "-m", "second")
	second := run("rev-parse", "HEAD")

	source, err := gitsource.New(root, "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// The same path, the same lines, two commits, two different answers.
	older, err := source.ReadRange(ctx, first, "internal/executive/orchestrator.go", 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := source.ReadRange(ctx, second, "internal/executive/orchestrator.go", 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(older, "driveDepartments()") || !strings.Contains(newer, "driveDepartmentsRenamed") {
		t.Fatalf("reading did not follow the commit: %q vs %q", older, newer)
	}

	// Discovery follows the commit too: the old name is not findable in the
	// new tree, which is how a stale suggestion fails safe rather than
	// producing evidence about code that no longer exists.
	hits, err := source.Search(ctx, second, "driveDepartmentsRenamed", 5)
	if err != nil || len(hits) != 1 || hits[0] != "internal/executive/orchestrator.go" {
		t.Fatalf("search at the new commit returned %v (err=%v)", hits, err)
	}

	// A whole excerpt, end to end, is citable and refuses the other commit.
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", second, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := explorer.Read(ctx, "internal/executive/orchestrator.go", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := fragment.Validate(); err != nil {
		t.Fatalf("a real read must produce a valid fragment: %v", err)
	}
	if err := fragment.ValidFor(first); err == nil {
		t.Fatal("an excerpt of the new commit must not stand as evidence about the old one")
	}
	if !strings.Contains(fragment.Reference(), second) {
		t.Fatalf("the citation must name the commit read: %s", fragment.Reference())
	}

	// A path that does not exist at that commit yields no evidence at all.
	if _, err := explorer.Read(ctx, "internal/executive/never-existed.go", 1, 5); err == nil {
		t.Fatal("a missing path must produce no evidence")
	}
	// And nothing outside the repository is reachable.
	if _, err := source.ReadRange(ctx, second, "../../../etc/passwd", 1, 1); err == nil {
		t.Fatal("a path escaping the repository must be refused")
	}
}
