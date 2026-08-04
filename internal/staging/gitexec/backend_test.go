package gitexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

func TestWorktreeSealAndAtomicPromotion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	workspaceRoot := filepath.Join(root, "workspaces")
	mustMkdir(t, repo)
	mustMkdir(t, workspaceRoot)
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	git(t, repo, "switch", "--detach")
	backend, err := New("git", workspaceRoot, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	config := staging.RepositoryConfig{ID: "repo", Path: repo, Enabled: true, AllowedTargetRefs: []string{"refs/heads/main"}}
	if err := backend.ValidateRepository(ctx, config); err != nil {
		t.Fatal(err)
	}
	ref := staging.WorkspaceRef{ID: 1, RepositoryID: "repo", WorkspaceKey: "workspace-1", WorkspacePath: filepath.Join(workspaceRoot, "workspace-1")}
	if err := backend.CreateWorktree(ctx, staging.CreateWorktreeRequest{Repository: config, Workspace: ref, BaseCommit: base}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ref.WorkspacePath, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sealed, err := backend.SealWorktree(ctx, staging.SealRequest{Repository: config, Workspace: ref, WorkspaceID: 1, TaskID: 10, AttemptID: 20, BaseCommit: base, TargetRef: "refs/heads/main", MaxChangedFiles: 10, MaxArtifactBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if sealed.CandidateCommit == base || len(sealed.Manifest) == 0 || len(sealed.Patch) == 0 || len(sealed.ChangedFiles) != 1 {
		t.Fatalf("unexpected sealed revision: %+v", sealed)
	}
	if target, err := backend.ReadTarget(ctx, config, "refs/heads/main"); err != nil || target != base {
		t.Fatalf("target changed before promotion: %s %v", target, err)
	}
	if err := backend.VerifySealedRevision(ctx, config, ref, base, sealed.CandidateCommit, sealed.CandidateTree); err != nil {
		t.Fatal(err)
	}
	tamper := filepath.Join(ref.WorkspacePath, "tamper.txt")
	if err := os.WriteFile(tamper, []byte("post-seal mutation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backend.VerifySealedRevision(ctx, config, ref, base, sealed.CandidateCommit, sealed.CandidateTree); !errors.Is(err, staging.ErrWorkspaceDirty) {
		t.Fatalf("dirty sealed workspace got %v", err)
	}
	if err := os.Remove(tamper); err != nil {
		t.Fatal(err)
	}
	result, err := backend.PromoteRef(ctx, staging.PromotionRefRequest{Repository: config, TargetRef: "refs/heads/main", CandidateCommit: sealed.CandidateCommit, ExpectedBaseCommit: base})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatal("promotion not applied")
	}
	second, err := backend.PromoteRef(ctx, staging.PromotionRefRequest{Repository: config, TargetRef: "refs/heads/main", CandidateCommit: sealed.CandidateCommit, ExpectedBaseCommit: base})
	if err != nil || !second.Applied {
		t.Fatalf("idempotent apply failed: %+v %v", second, err)
	}
}

func TestSealRejectsNoChangesAndUnsafeAttributes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	workspaceRoot := filepath.Join(root, "workspaces")
	mustMkdir(t, repo)
	mustMkdir(t, workspaceRoot)
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	git(t, repo, "switch", "--detach")
	backend, err := New("git", workspaceRoot, time.Second*20)
	if err != nil {
		t.Fatal(err)
	}
	config := staging.RepositoryConfig{ID: "repo", Path: repo, Enabled: true, AllowedTargetRefs: []string{"refs/heads/main"}}
	ref := staging.WorkspaceRef{ID: 2, RepositoryID: "repo", WorkspaceKey: "workspace-2", WorkspacePath: filepath.Join(workspaceRoot, "workspace-2")}
	if err := backend.CreateWorktree(ctx, staging.CreateWorktreeRequest{Repository: config, Workspace: ref, BaseCommit: base}); err != nil {
		t.Fatal(err)
	}
	_, err = backend.SealWorktree(ctx, staging.SealRequest{Repository: config, Workspace: ref, WorkspaceID: 2, TaskID: 1, AttemptID: 1, BaseCommit: base, TargetRef: "refs/heads/main", MaxChangedFiles: 10, MaxArtifactBytes: 1 << 20})
	if !errors.Is(err, staging.ErrNoChanges) {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(filepath.Join(ref.WorkspacePath, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = backend.SealWorktree(ctx, staging.SealRequest{Repository: config, Workspace: ref, WorkspaceID: 2, TaskID: 1, AttemptID: 1, BaseCommit: base, TargetRef: "refs/heads/main", MaxChangedFiles: 10, MaxArtifactBytes: 1 << 20})
	if !errors.Is(err, staging.ErrUnsafeRepository) {
		t.Fatalf("got %v", err)
	}
}

func TestPromotionRejectsCheckedOutTarget(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	workspaceRoot := filepath.Join(root, "workspaces")
	mustMkdir(t, repo)
	mustMkdir(t, workspaceRoot)
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a")
	git(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	backend, err := New("git", workspaceRoot, time.Second*20)
	if err != nil {
		t.Fatal(err)
	}
	config := staging.RepositoryConfig{ID: "repo", Path: repo, Enabled: true, AllowedTargetRefs: []string{"refs/heads/main"}}
	_, err = backend.PromoteRef(ctx, staging.PromotionRefRequest{Repository: config, TargetRef: "refs/heads/main", CandidateCommit: base, ExpectedBaseCommit: base})
	if !errors.Is(err, staging.ErrUnsafeRepository) {
		t.Fatalf("got %v", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, body)
	}
	return string(body)
}
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
