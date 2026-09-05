package skillpublisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
)

var (
	ErrRemoteRequired             = errors.New("remote name is required when RequireRemote is true")
	ErrRemoteAttestationFailed    = errors.New("remote publication attestation failed")
	ErrPublicationContentMismatch = errors.New("published git commit blob does not match requested bytes")
	ErrPublicationCollision       = errors.New("remote publication ref exists with different content (fail-closed)")
)

type GitPublisherConfig struct {
	RepoDir       string // SkillSourceRepoRoot (local clone or working tree)
	RemoteName    string // e.g. "origin"
	Branch        string // e.g. "main"
	Owner         string // "explorarte-org"
	Repo          string // "skills"
	RequireRemote bool   // when true, remote push and remote attestation are mandatory
}

// GitPublisher is a host-owned implementation of source.SourcePublisher.
// It manages commit creation, remote publication, crash recovery, and remote attestation.
// Skill Forge has zero Git authority and only talks to the source.SourcePublisher port.
type GitPublisher struct {
	cfg GitPublisherConfig
}

type LocalGitPublisher = GitPublisher

func NewGitPublisher(cfg GitPublisherConfig) (*GitPublisher, error) {
	if strings.TrimSpace(cfg.RepoDir) == "" {
		return nil, fmt.Errorf("repo directory is required")
	}
	abs, err := filepath.Abs(cfg.RepoDir)
	if err != nil {
		return nil, fmt.Errorf("resolve repo dir: %w", err)
	}
	cfg.RepoDir = abs

	if strings.TrimSpace(cfg.Owner) == "" || strings.TrimSpace(cfg.Repo) == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		cfg.Branch = "main"
	}
	if cfg.RequireRemote && strings.TrimSpace(cfg.RemoteName) == "" {
		return nil, ErrRemoteRequired
	}

	return &GitPublisher{cfg: cfg}, nil
}

func NewLocalGitPublisher(repoDir, owner, repo string) (*GitPublisher, error) {
	return NewGitPublisher(GitPublisherConfig{
		RepoDir:       repoDir,
		Owner:         owner,
		Repo:          repo,
		Branch:        "main",
		RequireRemote: false,
	})
}

func (p *GitPublisher) Publish(ctx context.Context, req source.PublishRequest) (source.PublishedSource, error) {
	if strings.TrimSpace(req.SkillID) == "" {
		return source.PublishedSource{}, fmt.Errorf("skill id is required")
	}
	if len(req.CandidateSourceBytes) == 0 {
		return source.PublishedSource{}, fmt.Errorf("source bytes cannot be empty")
	}

	rawSum := sha256.Sum256(req.CandidateSourceBytes)
	rawSHA := hex.EncodeToString(rawSum[:])
	if req.ExpectedContentDigest != "" && rawSHA != req.ExpectedContentDigest {
		return source.PublishedSource{}, fmt.Errorf("expected digest %s, computed %s", req.ExpectedContentDigest, rawSHA)
	}

	normBytes, err := document.NormalizeText(req.CandidateSourceBytes)
	if err != nil {
		return source.PublishedSource{}, fmt.Errorf("normalize candidate text: %w", err)
	}
	normSum := sha256.Sum256(normBytes)
	normSHA := hex.EncodeToString(normSum[:])

	relPath := filepath.ToSlash(filepath.Join("skills", req.SkillID, "SKILL.md"))
	fullPath := filepath.Join(p.cfg.RepoDir, filepath.FromSlash(relPath))

	// Deterministic publication key:
	// publication_key = SHA256(organization_id || skill_id || raw_source_sha256)
	orgID := ""
	if req.Metadata != nil {
		orgID = req.Metadata["organization_id"]
	}
	pubKeyPayload := orgID + req.SkillID + rawSHA
	pubKeySum := sha256.Sum256([]byte(pubKeyPayload))
	pubKey := hex.EncodeToString(pubKeySum[:])

	tagName := fmt.Sprintf("skillforge/%s", pubKey)
	tagRef := fmt.Sprintf("refs/tags/%s", tagName)

	// 1. Check remote immutable publication ref if remote is configured (fresh host clone recovery)
	var commitSHA string
	if strings.TrimSpace(p.cfg.RemoteName) != "" {
		lsCmd := exec.CommandContext(ctx, "git", "ls-remote", p.cfg.RemoteName, tagRef)
		lsCmd.Dir = p.cfg.RepoDir
		if out, err := lsCmd.Output(); err == nil {
			outputStr := strings.TrimSpace(string(out))
			if len(outputStr) >= 40 {
				remoteSHA := outputStr[:40]

				// Fetch exact ref to local object store
				fetchCmd := exec.CommandContext(ctx, "git", "fetch", p.cfg.RemoteName, fmt.Sprintf("%s:%s", tagRef, tagRef))
				fetchCmd.Dir = p.cfg.RepoDir
				_ = fetchCmd.Run()

				// Verify commit exists and content matches
				showCmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf("%s:%s", remoteSHA, relPath))
				showCmd.Dir = p.cfg.RepoDir
				existingBytes, err := showCmd.Output()
				if err == nil {
					existingRawSum := sha256.Sum256(existingBytes)
					existingRawSHA := hex.EncodeToString(existingRawSum[:])
					if existingRawSHA == rawSHA {
						// Content matches: reuse exact publication!
						return source.PublishedSource{
							OriginRef:        fmt.Sprintf("%s/%s@%s", p.cfg.Owner, p.cfg.Repo, remoteSHA),
							Path:             relPath,
							RawSHA256:        rawSHA,
							NormalizedSHA256: normSHA,
							PublicationRef:   tagRef,
						}, nil
					}
				}
				// Tag exists on remote with different SHA/content: FAIL CLOSED!
				return source.PublishedSource{}, fmt.Errorf("%w: remote publication tag %s points to sha %s with differing content", ErrPublicationCollision, tagRef, remoteSHA)
			}
		}
	}

	// 2. Check if local commit with this publication key already exists (crash recovery / retry)
	logCmd := exec.CommandContext(ctx, "git", "log", "-n", "1", "--grep=^Publication-Key: "+pubKey, "--format=%H")
	logCmd.Dir = p.cfg.RepoDir
	if out, err := logCmd.Output(); err == nil {
		existing := strings.TrimSpace(string(out))
		if len(existing) == 40 {
			showCmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf("%s:%s", existing, relPath))
			showCmd.Dir = p.cfg.RepoDir
			if existingBytes, err := showCmd.Output(); err == nil && string(existingBytes) == string(req.CandidateSourceBytes) {
				commitSHA = existing
			}
		}
	}

	// 3. If not found via publication key, write and commit locally
	if commitSHA == "" {
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return source.PublishedSource{}, fmt.Errorf("create skill dir: %w", err)
		}
		if err := os.WriteFile(fullPath, req.CandidateSourceBytes, 0644); err != nil {
			return source.PublishedSource{}, fmt.Errorf("write skill file: %w", err)
		}

		addCmd := exec.CommandContext(ctx, "git", "add", filepath.FromSlash(relPath))
		addCmd.Dir = p.cfg.RepoDir
		if out, err := addCmd.CombinedOutput(); err != nil {
			return source.PublishedSource{}, fmt.Errorf("git add failed: %v, output: %s", err, string(out))
		}

		diffCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
		diffCmd.Dir = p.cfg.RepoDir
		if err := diffCmd.Run(); err == nil {
			headRevCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
			headRevCmd.Dir = p.cfg.RepoDir
			if out, err := headRevCmd.Output(); err == nil {
				headSHA := strings.TrimSpace(string(out))
				showCmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf("%s:%s", headSHA, relPath))
				showCmd.Dir = p.cfg.RepoDir
				if existingBytes, err := showCmd.Output(); err == nil && string(existingBytes) == string(req.CandidateSourceBytes) {
					commitSHA = headSHA
				}
			}
		}

		if commitSHA == "" {
			commitMsg := fmt.Sprintf("chore(skills): publish %s candidate\n\nPublication-Key: %s", req.SkillID, pubKey)
			commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
			commitCmd.Dir = p.cfg.RepoDir
			if out, err := commitCmd.CombinedOutput(); err != nil {
				return source.PublishedSource{}, fmt.Errorf("git commit failed: %v, output: %s", err, string(out))
			}

			revCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
			revCmd.Dir = p.cfg.RepoDir
			out, err := revCmd.Output()
			if err != nil {
				return source.PublishedSource{}, fmt.Errorf("git rev-parse HEAD failed: %v", err)
			}
			commitSHA = strings.TrimSpace(string(out))
		}
	}

	if len(commitSHA) != 40 {
		return source.PublishedSource{}, fmt.Errorf("invalid commit sha: %q", commitSHA)
	}

	// 4. Remote publication and immutable attestation
	if p.cfg.RequireRemote && strings.TrimSpace(p.cfg.RemoteName) == "" {
		return source.PublishedSource{}, ErrRemoteRequired
	}

	if strings.TrimSpace(p.cfg.RemoteName) != "" {
		// A. Fast-forward push to branch on configured remote (NEVER --force)
		refspec := fmt.Sprintf("%s:refs/heads/%s", commitSHA, p.cfg.Branch)
		pushCmd := exec.CommandContext(ctx, "git", "push", p.cfg.RemoteName, refspec)
		pushCmd.Dir = p.cfg.RepoDir
		_ = pushCmd.Run() // May already be pushed or ahead; tag attestation is our real anchor

		// B. Create local lightweight tag skillforge/<publication_key> -> commitSHA
		tagCmd := exec.CommandContext(ctx, "git", "tag", tagName, commitSHA)
		tagCmd.Dir = p.cfg.RepoDir
		_ = tagCmd.Run()

		// C. Push tag to remote (NEVER --force)
		pushTagCmd := exec.CommandContext(ctx, "git", "push", p.cfg.RemoteName, tagRef)
		pushTagCmd.Dir = p.cfg.RepoDir
		if out, err := pushTagCmd.CombinedOutput(); err != nil {
			// Check if tag already exists on remote pointing to commitSHA
			lsCheckCmd := exec.CommandContext(ctx, "git", "ls-remote", p.cfg.RemoteName, tagRef)
			lsCheckCmd.Dir = p.cfg.RepoDir
			outCheck, checkErr := lsCheckCmd.Output()
			if checkErr != nil || !strings.Contains(string(outCheck), commitSHA) {
				return source.PublishedSource{}, fmt.Errorf("remote tag publication push failed: %v (output: %s)", err, string(out))
			}
		}

		// D. Remote Attestation: Verify remote repository actually contains exact immutable tag pointing to commitSHA
		lsCmd := exec.CommandContext(ctx, "git", "ls-remote", p.cfg.RemoteName, tagRef)
		lsCmd.Dir = p.cfg.RepoDir
		out, err := lsCmd.Output()
		if err != nil {
			return source.PublishedSource{}, fmt.Errorf("remote attestation ls-remote failed: %w", err)
		}
		remoteOutput := string(out)
		if !strings.Contains(remoteOutput, commitSHA) {
			return source.PublishedSource{}, fmt.Errorf("%w: commit %s not present at %s on remote %s", ErrRemoteAttestationFailed, commitSHA, tagRef, p.cfg.RemoteName)
		}
	} else if p.cfg.RequireRemote {
		return source.PublishedSource{}, ErrRemoteRequired
	} else {
		// Local-only tag
		tagCmd := exec.CommandContext(ctx, "git", "tag", tagName, commitSHA)
		tagCmd.Dir = p.cfg.RepoDir
		_ = tagCmd.Run()
	}

	// 5. Verify exact SHA:path bytes in the committed object
	showCmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf("%s:%s", commitSHA, relPath))
	showCmd.Dir = p.cfg.RepoDir
	verifiedBytes, err := showCmd.Output()
	if err != nil {
		return source.PublishedSource{}, fmt.Errorf("verify commit blob %s:%s: %w", commitSHA, relPath, err)
	}
	verifiedRawSum := sha256.Sum256(verifiedBytes)
	verifiedRawSHA := hex.EncodeToString(verifiedRawSum[:])
	if verifiedRawSHA != rawSHA {
		return source.PublishedSource{}, fmt.Errorf("%w: expected %s, verified %s", ErrPublicationContentMismatch, rawSHA, verifiedRawSHA)
	}

	originRef := fmt.Sprintf("%s/%s@%s", p.cfg.Owner, p.cfg.Repo, commitSHA)

	return source.PublishedSource{
		OriginRef:        originRef,
		Path:             relPath,
		RawSHA256:        rawSHA,
		NormalizedSHA256: normSHA,
		PublicationRef:   tagRef,
	}, nil
}
