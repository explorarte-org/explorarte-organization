package observe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// GitRef reports the commit a ref currently points at.
//
// The desired build is read from the ref rather than from the promotion
// record, because the ref is what a promotion actually moves and the record
// is only what it intended. An applied promotion whose ref was later moved by
// something else leaves the record saying one thing and the world holding
// another, and the world is the one that runs. This is the same reason
// ApplyPromotion refuses to move a target that has drifted from the base it
// expected instead of assuming its intent still describes reality.
type GitRef struct {
	RepoDir string
	Ref     string
}

func NewGitRef(repoDir, ref string) *GitRef { return &GitRef{RepoDir: repoDir, Ref: ref} }

func (g *GitRef) DesiredSHA(ctx context.Context) (string, error) {
	if strings.TrimSpace(g.RepoDir) == "" || strings.TrimSpace(g.Ref) == "" {
		return "", fmt.Errorf("a git ref reader needs a repository and a ref")
	}
	// --verify and the ^{commit} peel mean a ref that does not exist is an
	// error rather than an argument git helpfully reinterprets as a path.
	cmd := exec.CommandContext(ctx, "git", "-C", g.RepoDir, "rev-parse", "--verify", "--quiet", g.Ref+"^{commit}")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("resolving %s in %s: %w: %s", g.Ref, g.RepoDir, err, strings.TrimSpace(errOut.String()))
	}
	sha := strings.TrimSpace(out.String())
	if !shaRE.MatchString(sha) {
		return "", fmt.Errorf("resolving %s produced %q, which is not a commit SHA", g.Ref, sha)
	}
	return sha, nil
}
