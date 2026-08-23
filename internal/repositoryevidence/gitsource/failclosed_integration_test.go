//go:build integration

package gitsource_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence/gitsource"
)

// A broken sensor must never look like an empty repository.
//
// Both operations used to fold every git failure into "nothing found": an
// unreachable directory, a commit that does not exist, a git that will not
// run. The design phase would then receive little or no evidence and go back
// to guessing, with every component reporting success. Blindness has to be
// loud, because its symptom is silence.
func TestASensorFailureIsNotAnEmptyRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	const realSHA = "0000000000000000000000000000000000000000"

	// A directory that is not a repository at all.
	broken, err := gitsource.New(t.TempDir(), "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := broken.Search(ctx, realSHA, "Orchestrator", 5); !errors.Is(err, gitsource.ErrSourceUnavailable) {
		t.Fatalf("searching an unreadable repository must fail loudly, got %v", err)
	}
	if _, err := broken.Lines(ctx, realSHA, "internal/executive/orchestrator.go"); !errors.Is(err, gitsource.ErrSourceUnavailable) {
		t.Fatalf("a file length from an unreadable repository must fail loudly, got %v", err)
	}
	if _, err := broken.ReadRange(ctx, realSHA, "internal/executive/orchestrator.go", 1, 5); !errors.Is(err, gitsource.ErrSourceUnavailable) {
		t.Fatalf("reading from an unreadable repository must fail loudly, got %v", err)
	}

	// A git binary that does not exist is the same class of fact.
	missing, err := gitsource.New(t.TempDir(), "/nonexistent/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Search(ctx, realSHA, "anything", 5); !errors.Is(err, gitsource.ErrSourceUnavailable) {
		t.Fatalf("a missing git must fail loudly, got %v", err)
	}
}

// A term that genuinely appears nowhere is an ANSWER, and must stay one.
// If every empty result became an error the sensor would be useless: most
// searches legitimately find nothing.
func TestNothingFoundIsStillAnAnswer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(command.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "empty")
	shaOut, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := string(shaOut[:40])

	source, err := gitsource.New(root, "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := source.Search(context.Background(), sha, "ThisSymbolDoesNotExistAnywhere", 5)
	if err != nil {
		t.Fatalf("a term that appears nowhere is an answer, not a failure: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %v", matches)
	}
}
