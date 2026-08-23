package engineeringmission

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

// RepositoryCatalog resolves a repository ID to its trusted configuration.
type RepositoryCatalog interface {
	Get(context.Context, string) (staging.RepositoryConfig, string, error)
}

// TargetReader reports the commit a target ref points at.
type TargetReader interface {
	ReadTarget(context.Context, staging.RepositoryConfig, string) (string, error)
}

// StagingHead resolves the head of a mission target through the same catalog
// and git backend the staging service already uses.
//
// Recovery reads the head through this rather than shelling out on its own so
// that "what the target points at" has exactly one definition in the system.
// Two ways of answering that question would eventually disagree, and the
// disagreement would show up as a recovery episode pinned to a commit the
// runner then refuses to check out.
type StagingHead struct {
	Catalog RepositoryCatalog
	Backend TargetReader
}

func (h StagingHead) ResolveHead(ctx context.Context, repositoryID, targetRef string) (string, error) {
	if h.Catalog == nil || h.Backend == nil {
		return "", fmt.Errorf("head resolution requires a repository catalog and a git backend")
	}
	repository, _, err := h.Catalog.Get(ctx, repositoryID)
	if err != nil {
		return "", err
	}
	if !staging.TargetAllowed(repository, targetRef) {
		// A target the repository does not allow must never become the
		// base of a recovery mission: that would let recovery pin work to
		// a ref the promotion path itself would refuse.
		return "", fmt.Errorf("target ref %q is not allowed for repository %q", targetRef, repositoryID)
	}
	head, err := h.Backend.ReadTarget(ctx, repository, targetRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(head), nil
}

var _ HeadResolver = StagingHead{}
