package skillpublisher

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
)

// GitPinnedSourceReader extracts source artifacts directly from an exact, immutable
// git commit object in a local repository or mirror. It does NOT read working tree files,
// branch pointers, or perform network requests at runtime.
type GitPinnedSourceReader struct {
	RepoDir string
}

func NewGitPinnedSourceReader(repoDir string) (*GitPinnedSourceReader, error) {
	if strings.TrimSpace(repoDir) == "" {
		return nil, fmt.Errorf("repo dir is required")
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve repo dir: %w", err)
	}
	return &GitPinnedSourceReader{RepoDir: abs}, nil
}

func (r *GitPinnedSourceReader) ReadPinned(ctx context.Context, ref source.PinnedSourceRef) (source.PinnedSourceArtifact, error) {
	// Parse OriginRef: owner/repo@<40hex>
	parts := strings.Split(ref.OriginRef, "@")
	if len(parts) != 2 || len(parts[1]) != 40 {
		return source.PinnedSourceArtifact{}, fmt.Errorf("invalid origin ref %q: must be owner/repo@<40-hex-sha>", ref.OriginRef)
	}
	commitSHA := strings.TrimSpace(parts[1])

	cleanPath := filepath.Clean(strings.TrimSpace(ref.Path))
	if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return source.PinnedSourceArtifact{}, fmt.Errorf("path escapes root: %q", ref.Path)
	}

	// 1. Verify commit exists in repo object database
	catCmd := exec.CommandContext(ctx, "git", "cat-file", "-e", commitSHA+"^{commit}")
	catCmd.Dir = r.RepoDir
	if out, err := catCmd.CombinedOutput(); err != nil {
		return source.PinnedSourceArtifact{}, fmt.Errorf("%w: commit %s: %v (output: %s)", source.ErrPinnedCommitNotFound, commitSHA, err, string(out))
	}

	// 2. Read exact blob from commit:path directly using git show
	gitTarget := fmt.Sprintf("%s:%s", commitSHA, filepath.ToSlash(cleanPath))
	showCmd := exec.CommandContext(ctx, "git", "show", gitTarget)
	showCmd.Dir = r.RepoDir
	blobBytes, err := showCmd.Output()
	if err != nil {
		return source.PinnedSourceArtifact{}, fmt.Errorf("%w: path %q in commit %s: %w", source.ErrPinnedPathNotFound, cleanPath, commitSHA, err)
	}

	return source.PinnedSourceArtifact{
		CommitSHA: commitSHA,
		Path:      cleanPath,
		Bytes:     blobBytes,
	}, nil
}
