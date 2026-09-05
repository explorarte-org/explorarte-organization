package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
)

// LocalGitPublisher is a host-owned helper that publishes skills to a disposable
// or local git repository for testing and controlled environments.
// It executes strictly within the specified local repo without remote push authority.
type LocalGitPublisher struct {
	RepoDir string
	Owner   string
	Repo    string
}

func NewLocalGitPublisher(repoDir, owner, repo string) (*LocalGitPublisher, error) {
	if strings.TrimSpace(repoDir) == "" {
		return nil, fmt.Errorf("repo directory is required")
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve repo dir: %w", err)
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	return &LocalGitPublisher{
		RepoDir: abs,
		Owner:   strings.TrimSpace(owner),
		Repo:    strings.TrimSpace(repo),
	}, nil
}

func (p *LocalGitPublisher) Publish(ctx context.Context, req PublishRequest) (PublishedSource, error) {
	if strings.TrimSpace(req.SkillID) == "" {
		return PublishedSource{}, fmt.Errorf("skill id is required")
	}
	if len(req.CandidateSourceBytes) == 0 {
		return PublishedSource{}, fmt.Errorf("source bytes cannot be empty")
	}

	rawSum := sha256.Sum256(req.CandidateSourceBytes)
	rawSHA := hex.EncodeToString(rawSum[:])
	if req.ExpectedContentDigest != "" && rawSHA != req.ExpectedContentDigest {
		return PublishedSource{}, fmt.Errorf("expected digest %s, computed %s", req.ExpectedContentDigest, rawSHA)
	}

	normBytes, err := document.NormalizeText(req.CandidateSourceBytes)
	if err != nil {
		return PublishedSource{}, fmt.Errorf("normalize candidate text: %w", err)
	}
	normSum := sha256.Sum256(normBytes)
	normSHA := hex.EncodeToString(normSum[:])

	relPath := filepath.Join("skills", req.SkillID, "SKILL.md")
	fullPath := filepath.Join(p.RepoDir, relPath)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return PublishedSource{}, fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.WriteFile(fullPath, req.CandidateSourceBytes, 0644); err != nil {
		return PublishedSource{}, fmt.Errorf("write skill file: %w", err)
	}

	// Git add and commit in local repo
	addCmd := exec.CommandContext(ctx, "git", "add", relPath)
	addCmd.Dir = p.RepoDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return PublishedSource{}, fmt.Errorf("git add failed: %v, output: %s", err, string(out))
	}

	commitMsg := fmt.Sprintf("chore(skills): publish %s candidate", req.SkillID)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg, "--allow-empty")
	commitCmd.Dir = p.RepoDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return PublishedSource{}, fmt.Errorf("git commit failed: %v, output: %s", err, string(out))
	}

	revCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	revCmd.Dir = p.RepoDir
	out, err := revCmd.Output()
	if err != nil {
		return PublishedSource{}, fmt.Errorf("git rev-parse HEAD failed: %v", err)
	}
	commitSHA := strings.TrimSpace(string(out))
	if len(commitSHA) != 40 {
		return PublishedSource{}, fmt.Errorf("invalid commit sha returned: %q", commitSHA)
	}

	originRef := fmt.Sprintf("%s/%s@%s", p.Owner, p.Repo, commitSHA)

	return PublishedSource{
		OriginRef:        originRef,
		Path:             relPath,
		RawSHA256:        rawSHA,
		NormalizedSHA256: normSHA,
		PublicationRef:   fmt.Sprintf("git-commit:%s:%s", req.SkillID, commitSHA),
	}, nil
}
